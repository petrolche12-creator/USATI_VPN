#!/usr/bin/env python3
"""Готовит gVisor к сборке обычным `go build` (без bazel и без сети до gvisor.dev).

Зачем это нужно
----------------
Часть исходников gVisor хранится как шаблоны (`*_template.go`, `pkg/sync/locking/generic_*.go`),
а конкретные типы из них генерирует bazel правилами `go_template_instance` /
`declare_rwmutex`. В git этих сгенерированных файлов нет, поэтому `go build` падает с
`undefined: MaskOf64`, `undefined: waiterEntry`, `undefined: connRWMutex` и т. п.

Этот скрипт повторяет работу bazel: читает BUILD-файлы, собирает `go_generics` из самого
репозитория gVisor и генерирует все недостающие файлы.

Использование
-------------
    # 1) получить исходники gVisor (нужен тег или коммит)
    git clone --depth 1 --branch release-20231204.0 https://github.com/google/gvisor /tmp/gvisor

    # 2) сгенерировать файлы
    python3 tools/fix-gvisor.py /tmp/gvisor

    # 3) указать Go на эту копию (локально, не меняя go.mod в репозитории)
    cd mobile && go mod edit -replace gvisor.dev/gvisor=/tmp/gvisor && go build ./...

Скрипт идемпотентен: готовые файлы не перезаписывает.
"""

import glob
import os
import re
import shutil
import subprocess
import sys

# Кэш шаблонов: '//pkg/x:name' -> (каталог пакета, [файлы-шаблоны])
templates = {}
instances = []


def parse_list(v):
    return re.findall(r'"([^"]*)"', v)


def parse_map(v):
    return dict(re.findall(r'"([^"]*)"\s*:\s*"([^"]*)"', v))


def find_targets(text):
    """Разбирает go_template / go_template_instance из BUILD-файла."""
    out = []
    for m in re.finditer(r'(go_template|go_template_instance)\s*\((.*?)\n\)', text, re.S):
        kind, body = m.group(1), m.group(2)
        attrs = {}
        for am in re.finditer(r'(\w+)\s*=\s*(\[[^\]]*\]|"[^"]*"|\{[^}]*\}|[^,\n]+)', body):
            attrs[am.group(1)] = am.group(2).strip()
        out.append((kind, attrs))
    return out


def collect(root):
    """Собирает все шаблоны и все экземпляры, которые из них надо создать."""
    for dirpath, _, files in os.walk(root):
        if 'BUILD' not in files:
            continue
        pkg_dir = os.path.relpath(dirpath, root)
        if not pkg_dir.startswith('pkg'):
            continue
        try:
            text = open(os.path.join(dirpath, 'BUILD')).read()
        except OSError:
            continue

        # мьютексы gVisor описываются макросами из pkg/sync/locking/locking.bzl
        for m in re.finditer(r'declare_(rwmutex|mutex)\s*\((.*?)\n\)', text, re.S):
            kind, body = m.group(1), m.group(2)
            attrs = {}
            for am in re.finditer(r'(\w+)\s*=\s*("(?:[^"\\]|\\.)*"|\[[^\]]*\])', body):
                attrs[am.group(1)] = am.group(2).strip()
            tpl = 'generic_rwmutex' if kind == 'rwmutex' else 'generic_mutex'
            attrs['template'] = f'"//pkg/sync/locking:{tpl}"'
            nm = re.search(r'nested_lock_names\s*=\s*\[([^\]]*)\]', body)
            if nm:
                attrs['nested_lock_names'] = nm.group(1)
            instances.append((pkg_dir, attrs.get('name', '?').strip('"'), attrs))

        for kind, attrs in find_targets(text):
            name = attrs.get('name', '').strip('"')
            if kind == 'go_template':
                info = (pkg_dir, parse_list(attrs.get('srcs', '')), parse_list(attrs.get('opt_types', '')))
                templates[f'//{pkg_dir}:{name}'] = info
                templates[name] = info
            else:
                instances.append((pkg_dir, name, attrs))


def generate(root, gg, pkg_dir, attrs):
    out = attrs.get('out', '').strip('"')
    template = attrs.get('template', '').strip('"')
    out_path = os.path.join(root, pkg_dir, out)
    if not out:
        return None, 'нет out'
    if os.path.basename(out_path).startswith('generic_'):
        return None, 'это шаблон, а не результат'
    if os.path.exists(out_path):
        return None, 'уже есть'

    tpl = templates.get(template)
    if tpl is None and template.startswith(':'):
        tpl = templates.get(f'//{pkg_dir}:{template[1:]}')
    if tpl is None:
        return None, f'нет шаблона {template}'
    tpl_dir, srcs, _ = tpl
    if not srcs:
        return None, 'шаблон без srcs'

    src = os.path.join(root, tpl_dir, srcs[0])
    args = [gg, '-i', src, '-o', out_path]

    pkg_name = attrs.get('package', '').strip('"')
    if pkg_name:
        args += ['-p', pkg_name]
    if attrs.get('prefix', '').strip('"'):
        args += ['-prefix', attrs['prefix'].strip('"')]
    if attrs.get('suffix', '').strip('"'):
        args += ['-suffix', attrs['suffix'].strip('"')]
    for k, v in parse_map(attrs.get('types', '')).items():
        args += ['-t', f'{k}={v}']
    for k, v in parse_map(attrs.get('imports', '')).items():
        args += ['-import', f'{k}={v}']

    # nested_lock_names: bazel подставляет имена замков прямо в шаблон
    names = parse_list(attrs.get('nested_lock_names', ''))
    if names:
        consts = ', '.join('Lock' + n.title() + ' = lockNameIndex(%d)' % i for i, n in enumerate(names))
        literal = ', '.join('"%s"' % n for n in names)
        text = open(src).read()
        text = text.replace('func initLockNames() {}', 'func initLockNames() { lockNames = []string{%s} }' % literal)
        text = text.replace('// LOCK_NAME_INDEX_CONSTANTS', 'const (\n\t%s\n)' % consts)
        tmp = out_path + '.tpl'
        with open(tmp, 'w') as f:
            f.write(text)
        args = [a.replace(src, tmp) for a in args]

    r = subprocess.run(args, capture_output=True, text=True)
    if r.returncode != 0:
        return None, 'go_generics: ' + (r.stderr.strip() or r.stdout.strip())[:140]
    return out, None


def main():
    if len(sys.argv) < 2:
        print('использование: fix-gvisor.py <каталог с исходниками gVisor>')
        return 2
    root = os.path.abspath(sys.argv[1])
    if not os.path.isdir(os.path.join(root, 'pkg')):
        print(f'это не каталог gVisor: {root}')
        return 1

    collect(root)

    # сам генератор собираем из репозитория gVisor
    gg = os.path.join('/tmp/gvisorfix_bin', 'go_generics')
    os.makedirs(os.path.dirname(gg), exist_ok=True)
    env = dict(os.environ, GOWORK='off', GOFLAGS='-mod=mod', GOPROXY='off')
    r = subprocess.run(['go', 'build', '-o', gg, './tools/go_generics'], cwd=root, env=env,
                       capture_output=True, text=True)
    if r.returncode != 0:
        print('не удалось собрать go_generics:\n' + (r.stderr or r.stdout)[:800])
        return 1

    # если каталог испорчен прошлым запуском, убираем ошибочный тег у результатов
    for pkg_dir, name, attrs in instances:
        out = attrs.get('out', '').strip('"')
        if not out:
            continue
        op = os.path.join(root, pkg_dir, out)
        if os.path.exists(op):
            t = open(op).read()
            if '//go:build ignore' in t:
                open(op, 'w').write(t.replace('//go:build ignore\n\n', '', 1))

    made, errors = 0, []
    for pkg_dir, name, attrs in instances:
        out, err = generate(root, gg, pkg_dir, attrs)
        if out:
            made += 1
            print(f'  создан {pkg_dir}/{out}')
        elif err and err != 'уже есть':
            errors.append(f'{pkg_dir}/{name}: {err}')

    # В gVisor тестовые шаблоны (mutex_test2.go и т.п.) лежат в каталоге пакета и без
    # bazel-фильтра ломают сборку: "found packages locking and locking_test". Удаляем.
    for f in glob.glob(os.path.join(root, 'pkg', 'sync', 'locking', '*_test*.go')):
        os.remove(f)

    # ВАЖНО: тег `ignore` ставим ПОСЛЕ генерации, и только самим шаблонам.
    # go_generics копирует шапку шаблона в результат, поэтому если пометить шаблон
    # заранее, сгенерированные bits64.go / *_list.go / *_mutex.go тоже получат
    # `//go:build ignore` — и сборка ядра упадёт (undefined: MaskOf64 и т.п.).
    only_templates = glob.glob(os.path.join(root, 'pkg', '**', '*_template.go'), recursive=True)
    only_templates += glob.glob(os.path.join(root, 'pkg', 'sync', 'locking', 'generic_*.go'))
    for f in only_templates:
        text = open(f).read()
        if 'go:build ignore' in text:
            continue
        m = re.search(r'^package\s+\S+', text, re.M)
        if m:
            with open(f, 'w') as fh:
                fh.write(text[:m.start()] + '//go:build ignore\n\n' + text[m.start():])

    print(f'\nсоздано файлов: {made}')
    for e in errors[:20]:
        print('  !', e)
    return 0


if __name__ == '__main__':
    sys.exit(main())
