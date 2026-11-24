#!/usr/bin/env python3
"""Проверка собранного classes.dex перед подписью APK.

Ловит то, из-за чего приложение молча падает при запуске:
  1) нет ОПРЕДЕЛЕНИЙ обязательных классов (kotlin-stdlib, JSch, наши экраны) —
     строка «Lkotlin/...» в dex ещё ничего не значит, она может быть просто ссылкой;
  2) есть ссылки на классы, которых нет ни в dex, ни в android.jar —
     на телефоне это NoClassDefFoundError.

Запуск: python3 tools/check-apk.py path/to/classes.dex path/to/android.jar
"""

import struct
import sys
import zipfile

# Классы, которые должны быть ОПРЕДЕЛЕНЫ в dex, иначе приложение не запустится.
REQUIRED = [
    'Lkotlin/jvm/internal/Intrinsics;',   # kotlin-stdlib внутри dex
    'Lkotlin/Unit;',
    'Lkotlin/collections/CollectionsKt;',
    'Lkotlin/text/StringsKt;',
    'Lcom/jcraft/jsch/JSch;',             # SSH-установка на сервер
    'Lru/usatyi/vpn/MainActivity;',
    'Lru/usatyi/vpn/ChainService;',
    'Lru/usatyi/vpn/TunnelService;',
    'Lru/usatyi/vpn/CoreProcess;',
    'Lru/usatyi/vpn/CardView;',           # свои виджеты из layout
    'Lru/usatyi/vpn/BottomBar;',
    'Lru/usatyi/vpn/ProgressLine;',
    'Lru/usatyi/vpn/binding/ScreenConnectBinding;',
]

# Свои виджеты обязаны иметь конструктор (Context, AttributeSet), иначе LayoutInflater
# падает с InstantiationException прямо при открытии экрана.
REQUIRED_CTORS = {
    'Lru/usatyi/vpn/CardView;': '(Landroid/content/Context;Landroid/util/AttributeSet;)V',
    'Lru/usatyi/vpn/BottomBar;': '(Landroid/content/Context;Landroid/util/AttributeSet;)V',
    'Lru/usatyi/vpn/ProgressLine;': '(Landroid/content/Context;Landroid/util/AttributeSet;)V',
}

# Ссылки на классы этих пакетов проверяем отдельно.
CHECK_PREFIXES = ('Lkotlin/', 'Lru/usatyi/', 'Lcom/jcraft/')


def read_dex(data):
    """Возвращает (определённые классы, все упомянутые типы)."""
    def u4(off):
        return struct.unpack_from('<I', data, off)[0]

    string_ids_size, string_ids_off = u4(0x38), u4(0x3C)
    type_ids_size, type_ids_off = u4(0x40), u4(0x44)
    class_defs_size, class_defs_off = u4(0x60), u4(0x64)

    def string(i):
        p = u4(string_ids_off + 4 * i)
        while data[p] & 0x80:
            p += 1
        p += 1
        return data[p:data.index(b'\0', p)].decode('utf-8', 'replace')

    def type_name(i):
        return string(u4(type_ids_off + 4 * i))

    defined = {type_name(u4(class_defs_off + 32 * i)) for i in range(class_defs_size)}
    mentioned = {type_name(i) for i in range(type_ids_size)}
    return defined, mentioned


def main() -> int:
    if len(sys.argv) < 2:
        print('использование: check-apk.py classes.dex [android.jar]')
        return 2
    with open(sys.argv[1], 'rb') as f:
        data = f.read()
    if not data.startswith(b'dex\n'):
        print('это не dex-файл')
        return 1

    defined, mentioned = read_dex(data)
    check_ctors = '--no-ctors' not in sys.argv

    platform = set()
    if len(sys.argv) > 2:
        with zipfile.ZipFile(sys.argv[2]) as z:
            platform = {'L' + n[:-6] + ';' for n in z.namelist() if n.endswith('.class')}

    missing = [c for c in REQUIRED if c not in defined]
    bad_ctor = []
    if check_ctors:
        try:
            import logging
            logging.disable(logging.CRITICAL)
            from androguard.core.dex import DEX  # необязательная зависимость

            dex = DEX(data)
            sigs = {}
            for c in dex.get_classes():
                # androguard печатает дескрипторы с пробелами после ';' — убираем их
                sigs[c.get_name()] = {
                    m.get_descriptor().replace(' ', '') for m in c.get_methods() if m.get_name() == '<init>'
                }
            for cls, sig in REQUIRED_CTORS.items():
                if cls in defined and sig not in sigs.get(cls, set()):
                    bad_ctor.append(f'{cls} нет конструктора {sig}')
        except ImportError:
            print('(конструкторы View не проверены: нет пакета androguard)')
    # ссылки на свои/внешние классы, которых нет ни в dex, ни в android.jar
    dangling = sorted(
        t for t in mentioned
        if t.startswith(CHECK_PREFIXES) and t not in defined and t not in platform
    )

    print(f'классов определено в dex: {len(defined)} (упомянуто типов: {len(mentioned)})')
    if missing:
        print('НЕТ определений обязательных классов:')
        for c in missing:
            print('  ', c)
    if dangling:
        print('ССЫЛКИ на отсутствующие классы (на телефоне будет NoClassDefFoundError):')
        for c in dangling[:20]:
            print('  ', c)
        if len(dangling) > 20:
            print(f'   … и ещё {len(dangling) - 20}')
    if bad_ctor:
        print('НЕТ обязательных конструкторов (LayoutInflater упадёт):')
        for c in bad_ctor:
            print('  ', c)
    if missing or dangling or bad_ctor:
        return 1
    print('проверка пройдена: все обязательные классы на месте, «висячих» ссылок нет')
    return 0


if __name__ == '__main__':
    sys.exit(main())
