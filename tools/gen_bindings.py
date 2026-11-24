#!/usr/bin/env python3
"""Генератор ViewBinding-классов вместо Gradle.

Gradle с включённым viewBinding создаёт по классу на каждый layout (ScreenConnectBinding и т. д.)
с полями для всех @+id. Здесь то же самое, но без Gradle: классы пишутся в
android/app/src/main/java/ru/usatyi/vpn/binding, а inflate() находит виджеты по id в рантайме.

Запуск: python3 tools/gen_bindings.py
"""

import glob
import os
import re
import sys
import xml.etree.ElementTree as ET

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RES = os.path.join(ROOT, 'android/app/src/main/res/layout')
OUT = os.path.join(ROOT, 'android/app/src/main/java/ru/usatyi/vpn/binding')

# Тег layout → Kotlin-тип. Незнакомые теги (свои виджеты) берём как есть по простому имени.
KNOWN = {
    'TextView': 'android.widget.TextView',
    'EditText': 'android.widget.EditText',
    'Button': 'android.widget.Button',
    'RadioGroup': 'android.widget.RadioGroup',
    'RadioButton': 'android.widget.RadioButton',
    'LinearLayout': 'android.widget.LinearLayout',
    'FrameLayout': 'android.widget.FrameLayout',
    'ScrollView': 'android.widget.ScrollView',
    'ProgressBar': 'android.widget.ProgressBar',
    'Switch': 'android.widget.Switch',
    'CheckBox': 'android.widget.CheckBox',
    'ImageView': 'android.widget.ImageView',
    'Space': 'android.view.View',
    'View': 'android.view.View',
    'include': 'android.view.View',
}


def camel(name: str) -> str:
    parts = name.split('_')
    return parts[0] + ''.join(p[:1].upper() + p[1:] for p in parts[1:])


def className(layout: str) -> str:
    return ''.join(p[:1].upper() + p[1:] for p in layout.split('_')) + 'Binding'


def ktype(tag: str) -> str:
    tag = tag.split('.')[-1]
    if tag in KNOWN:
        return KNOWN[tag]
    # свои виджеты и чужие классы: ru.usatyi.vpn.CardView, BottomBar, ProgressLine
    if tag in ('CardView', 'BottomBar', 'ProgressLine'):
        return 'ru.usatyi.vpn.' + tag
    return 'android.view.View'


def ids_and_types(path):
    tree = ET.parse(path)
    found = {}
    for el in tree.iter():
        tag = el.tag.split('}')[-1]
        for k, v in el.attrib.items():
            if k.endswith('}id') and v.startswith('@+id/'):
                name = v[len('@+id/'):]
                if name == 'root':
                    continue
                found[name] = ktype(tag)
    return found


def main() -> int:
    os.makedirs(OUT, exist_ok=True)
    written = []
    for path in sorted(glob.glob(os.path.join(RES, '*.xml'))):
        layout = os.path.basename(path)[:-4]
        cls = className(layout)
        fields = ids_and_types(path)
        lines = [
            '// Сгенерировано tools/gen_bindings.py — не редактировать вручную.',
            'package ru.usatyi.vpn.binding',
            '',
            'import ru.usatyi.vpn.R',
            '',
        ]
        used = sorted(set(fields.values()))
        for t in used:
            lines.append(f'import {t}')
        lines += [
            '',
            f'/ ** {layout}.xml */'.replace('/ **', '/**'),
            f'class {cls}(val root: android.view.View) {{',
        ]
        for name, t in sorted(fields.items()):
            short = t.rsplit('.', 1)[-1]
            lines.append(f'    val {camel(name)}: {short} = root.findViewById(R.id.{name})')
        lines += [
            '',
            '    companion object {',
            f'        fun inflate(inflater: android.view.LayoutInflater): {cls} =',
            f'            {cls}(inflater.inflate(R.layout.{layout}, null, false))',
            '',
            f'        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): {cls} =',
            f'            {cls}(inflater.inflate(R.layout.{layout}, parent, false))',
            '',
            f'        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): {cls} =',
            f'            {cls}(inflater.inflate(R.layout.{layout}, parent, attach))',
            '    }',
            '}',
            '',
        ]
        out = os.path.join(OUT, cls + '.kt')
        with open(out, 'w') as f:
            f.write('\n'.join(lines))
        written.append((cls, len(fields)))
    print('Сгенерировано классов:', len(written))
    for cls, n in written:
        print(f'  {cls}: {n} полей')
    return 0


if __name__ == '__main__':
    sys.exit(main())
