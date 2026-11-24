#!/usr/bin/env python3
"""Собирает локальный GOPROXY (file://) из архивов GitHub.

Нужно, чтобы собирать проект без доступа к proxy.golang.org: модули берутся
с codeload.github.com и перепаковываются в канонический формат zip-модуля Go.
"""
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import time
import zipfile

PROXY = os.environ.get("PROXY_DIR", "/tmp/goproxy")
CACHE = "/tmp/modcache"
SKIP_PARTS = {".bzr", ".git", ".hg", ".svn"}

# module path -> (github owner/repo, subdir внутри репозитория)
MANUAL = {
    "github.com/amnezia-vpn/amneziawg-go/v3": ("amnezia-vpn/amneziawg-go", "", "v3.1.20261116", ""),
    "golang.org/x/crypto": ("golang/crypto", "", ""),
    "golang.org/x/net": ("golang/net", "", ""),
    "golang.org/x/sys": ("golang/sys", "", ""),
    "golang.org/x/text": ("golang/text", "", ""),
    "golang.org/x/time": ("golang/time", "", ""),
    "golang.org/x/exp": ("golang/exp", "", ""),
    "golang.org/x/mod": ("golang/mod", "", ""),
    "golang.org/x/tools": ("golang/tools", "", ""),
    "golang.org/x/sync": ("golang/sync", "", ""),
    "golang.org/x/term": ("golang/term", "", ""),
    "golang.org/x/mobile": ("golang/mobile", "", ""),
    "golang.org/x/image": ("golang/image", "", ""),
    "go.uber.org/atomic": ("uber-go/atomic", "", ""),
    "go.uber.org/mock": ("uber/mock", "", ""),
    "go.uber.org/zap": ("uber-go/zap", "", ""),
    "go.uber.org/multierr": ("uber-go/multierr", "", ""),
    "golang.zx2c4.com/wintun": ("WireGuard/wintun", "", ""),
    "golang.zx2c4.com/wireguard": ("WireGuard/wireguard-go", "", ""),
    "gvisor.dev/gvisor": ("google/gvisor", "", ""),
    "golang.getoutline.org/sdk": ("Jigsaw-Code/outline-sdk", "", ""),
    "golang.getoutline.org/sdk/x": ("Jigsaw-Code/outline-sdk", "x", ""),
    "github.com/akavel/rsrc": ("akavel/rsrc", "", ""),}


def repo_for(module):
    if module in MANUAL:
        return MANUAL[module]
    m = re.match(r"github\.com/([^/]+)/([^/]+)(/.*)?$", module)
    if m:
        return (m.group(1) + "/" + m.group(2), (m.group(3) or "").strip("/"), "")
    raise SystemExit("не знаю репозиторий для модуля " + module)


def ref_for(version, major_path=""):
    """Тег или коммит для версии модуля."""
    if major_path:
        return major_path, None
    m = re.match(r"^v\d+\.\d+\.\d+-(\d{14})-([0-9a-f]+)$", version)
    if m:
        return m.group(2), m.group(1)
    m = re.match(r"^v(\d+\.\d+\.\d+.*)$", version)
    if m:
        # для модулей с путём /vN тег по-прежнему vX.Y.Z
        return "v" + m.group(1) if not major_path else "v" + version.lstrip("v"), None
    return version, None


def download(owner_repo, ref):
    os.makedirs(CACHE, exist_ok=True)
    path = os.path.join(CACHE, owner_repo.replace("/", "_") + "_" + ref + ".tar.gz")
    if os.path.exists(path) and os.path.getsize(path) > 0:
        return path
    url = "https://codeload.github.com/%s/tar.gz/%s" % (owner_repo, ref)
    for attempt in range(3):
        r = subprocess.run(["curl", "-sSL", "--fail", "-o", path, url])
        if r.returncode == 0:
            return path
        time.sleep(2)
    raise SystemExit("не скачался архив " + url)


def proxy_dir(module):
    # кодирование пути как в модульном прокси: заглавные буквы -> !строчные
    enc = re.sub(r"[A-Z]", lambda m: "!" + m.group(0).lower(), module)
    return os.path.join(PROXY, enc, "@v")


def ensure_mod(module, version):
    """Кладёт <version>.mod и <version>.info в прокси (без zip)."""
    d = proxy_dir(module)
    os.makedirs(d, exist_ok=True)
    mod_path = os.path.join(d, version + ".mod")
    if os.path.exists(mod_path):
        return
    owner_repo, subdir, tag = repo_for(module)[:3]
    ref, stamp = ref_for(version, tag)
    tar = download(owner_repo, ref)
    with tarfile.open(tar, "r:gz") as tf:
        root = tf.getnames()[0].split("/")[0]
        member = root + ("/" + subdir if subdir else "") + "/go.mod"
        try:
            f = tf.extractfile(member)
        except KeyError:
            f = None
        if f is None:
            raise SystemExit("в %s@%s нет go.mod (%s)" % (module, version, member))
        data = f.read()
    with open(mod_path, "wb") as fh:
        fh.write(data)
    when = "1970-01-01T00:00:00Z"
    if stamp:
        when = "%s-%s-%sT%s:%s:%sZ" % (stamp[0:4], stamp[4:6], stamp[6:8], stamp[8:10], stamp[10:12], stamp[12:14])
    with open(os.path.join(d, version + ".info"), "w") as fh:
        json.dump({"Version": version, "Time": when}, fh)


def zip_is_valid(path, module, version):
    try:
        with zipfile.ZipFile(path) as z:
            names = z.namelist()
            prefix = module + "@" + version + "/"
            return bool(names) and all(n.startswith(prefix) for n in names)
    except Exception:
        return False


def ensure_zip(module, version):
    """Кладёт канонический <version>.zip модуля в прокси."""
    d = proxy_dir(module)
    os.makedirs(d, exist_ok=True)
    zip_path = os.path.join(d, version + ".zip")
    if os.path.exists(zip_path) and zip_is_valid(zip_path, module, version):
        return
    owner_repo, subdir, tag = repo_for(module)[:3]
    ref, _ = ref_for(version, tag)
    tar = download(owner_repo, ref)
    tmp = zip_path + ".tmp"
    prefix = module + "@" + version + "/"
    with tarfile.open(tar, "r:gz") as tf:
        root = tf.getnames()[0].split("/")[0]
        base = root + ("/" + subdir if subdir else "") + "/"
        members = {}
        nested_mod_dirs = set()
        for m in tf.getmembers():
            n = m.name
            if not n.startswith(base) or not m.isfile() or m.issym() or m.islnk():
                continue
            rel = n[len(base):]
            if not rel:
                continue
            parts = rel.split("/")
            if any(p in SKIP_PARTS for p in parts):
                continue
            if rel == "go.mod" or (len(parts) > 1 and "go.mod" in tf.getnames() ):
                pass
            members[rel] = m
        # вложенные модули (каталоги со своим go.mod) в архив модуля не входят
        for rel, m in list(members.items()):
            if rel.endswith("/go.mod"):
                nested_mod_dirs.add(rel[: -len("/go.mod")])
        for rel in list(members.keys()):
            if any(rel == nd or rel.startswith(nd + "/") for nd in nested_mod_dirs):
                del members[rel]
        with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as z:
            for rel in sorted(members):
                f = tf.extractfile(members[rel])
                data = f.read()
                zi = zipfile.ZipInfo(prefix + rel, date_time=(1980, 1, 1, 0, 0, 0))
                zi.external_attr = 0o644 << 16
                zi.compress_type = zipfile.ZIP_DEFLATED
                z.writestr(zi, data)
    os.replace(tmp, zip_path)


def main():
    if len(sys.argv) < 3:
        print("usage: proxy_gen.py <mod|zip> module@version [...]")
        return 2
    mode = sys.argv[1]
    for arg in sys.argv[2:]:
        module, version = arg.rsplit("@", 1)
        if mode == "mod":
            ensure_mod(module, version)
        else:
            ensure_mod(module, version)
            ensure_zip(module, version)
        print("ok", module, version)
    return 0


if __name__ == "__main__":
    sys.exit(main())
