#!/usr/bin/env python3
"""Добивает go mod tidy, докачивая недостающие модули в локальный file:// прокси."""
import re
import subprocess
import sys

PROXY = "/tmp/goproxy"
ENV = {
    "PATH": "/tmp/gobin-pkg/go/bin:/usr/bin:/bin",
    "GOPATH": "/tmp/gopath",
    "GOPROXY": "file:///tmp/goproxy",
    "GOFLAGS": "-mod=mod",
    "GOSUMDB": "off",
    "GONOSUMDB": "*",
    "GONOSUMCHECK": "1",
    "GOWORK": "off",
}


def fetch(mod, ver, full):
    mode = "zip" if full else "mod"
    cmd = [sys.executable, "/home/user/USATI_VPN/tools/proxy_gen.py", mode, mod + "@" + ver]
    subprocess.run(cmd, check=True, env={"PROXY_DIR": PROXY, "PATH": "/usr/bin:/bin"})


def main():
    cwd = sys.argv[1]
    for i in range(80):
        r = subprocess.run(["go", "mod", "tidy"], cwd=cwd, env=ENV, capture_output=True, text=True)
        out = r.stderr + r.stdout
        if r.returncode == 0:
            print("tidy ok")
            return 0
        pairs = []
        for m in re.finditer(r"([\w./\-]+)@(v[\w.\-+]+): (?:reading|missing)", out):
            pairs.append((m.group(1), m.group(2), "zip"))
        for m in re.finditer(r"(?:missing|reading) .*?mod/([\w./\-]+)@(v[\w.\-+]+)", out):
            pairs.append((m.group(1), m.group(2), "zip"))
        for m in re.finditer(r"([\w./\-]+)@(v[\w.\-+]+): no such file", out):
            pairs.append((m.group(1), m.group(2), "zip"))
        # нет .mod — сначала пробуем только mod-файл
        for m in re.finditer(r"file:///tmp/goproxy/([\w./\-!]+)/@v/(v[\w.\-+]+)\.mod: no such file", out):
            pairs.append((m.group(1).replace("!", ""), m.group(2), "mod"))
        for m in re.finditer(r"file:///tmp/goproxy/([\w./\-!]+)/@v/(v[\w.\-+]+)\.zip", out):
            pairs.append((m.group(1).replace("!", ""), m.group(2), "zip"))
        if not pairs:
            print("БЕЗ ПАР:\n" + out[-3000:])
            return 1
        seen = set()
        for mod, ver, kind in pairs:
            if (mod, ver) in seen:
                continue
            seen.add((mod, ver))
            try:
                fetch(mod, ver, kind == "zip")
                print("  +", kind, mod, ver)
            except Exception as e:
                print("  ! не смог", mod, ver, e)
        if not seen:
            print(out[-3000:])
            return 1
    print("слишком много итераций")
    return 1


if __name__ == "__main__":
    sys.exit(main())
