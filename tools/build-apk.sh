#!/bin/sh
# Сборка APK без Gradle и Android SDK: aapt2 + kotlinc + d8 + apksigner.
#
# Зачем: Gradle тянет AndroidX/Material из Maven, а в этой среде Maven недоступен.
# Приложение переведено на системные API, поэтому нужны только:
#   • android.jar        — из пакета @drxiaozhi/minapk (npm) или Android SDK
#   • aapt2 (linux)      — из пакета aaptjs3 (npm)
#   • d8.jar, apksigner.jar, ecj.jar — из пакета @drxiaozhi/minapk (npm)
#   • kotlinc            — из пакета kotlin-compiler (npm)
#   • JDK (java)         — достаточно JRE: javac не нужен, R.java собирает ECJ
#   • JSch 0.1.55        — из пакета cordova-plugin-ssh-connect (npm)
#
# Переменные окружения:
#   TOOLS=/path/to/tools   где лежат android.jar, d8.jar, apksigner.jar, ecj-3.45.0.jar
#   AAPT2=/path/to/aapt2   исполняемый aapt2 для Linux
#   KOTLINC=/path/to/kotlinc
#   JSCH=/path/to/jsch-0.1.55.jar
#   KOTLIN_STDLIB=/path/to/kotlin-stdlib.jar   (по умолчанию рядом с kotlinc)
#
#   sh tools/build-apk.sh dist/UsatyiVPN.apk
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:-$ROOT/dist/UsatyiVPN.apk}"
APP="$ROOT/android/app/src/main"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

: "${TOOLS:?укажите TOOLS с android.jar, d8.jar, apksigner.jar, ecj-3.45.0.jar}"
: "${AAPT2:?укажите AAPT2 (aapt2 для Linux)}"
: "${KOTLINC:?укажите KOTLINC}"
: "${JSCH:?укажите JSCH (jsch-0.1.55.jar)}"
JAVA="${JAVA:-java}"
# kotlinc ищет java в PATH: подкладываем каталог с нужным JAVA
export PATH="$(dirname "$(command -v "$JAVA" || echo "$JAVA")"):$PATH"
KS="${KS:-$ROOT/android/app/usatyi-release.jks}"
KS_PASS="${KS_PASS:-usatyi-vpn-2026}"
KS_ALIAS="${KS_ALIAS:-usatyi}"

# kotlin-stdlib обязателен: без него приложение падает при старте
# (NoClassDefFoundError: kotlin/jvm/internal/Intrinsics).
KOTLIN_STDLIB="${KOTLIN_STDLIB:-$(cd "$(dirname "$KOTLINC")/../lib" && pwd)/kotlin-stdlib.jar}"
test -f "$KOTLIN_STDLIB" || { echo "не найден kotlin-stdlib: $KOTLIN_STDLIB"; exit 1; }

CORE="$APP/jniLibs/arm64-v8a/libusatyi_core.so"
if [ "${BUILD_CORE:-1}" = "1" ]; then
	echo "== ядро цепочки (arm64)"
	OUT_JNI="$APP/jniLibs" sh "$ROOT/mobile/build-android.sh"
fi
test -f "$CORE" || { echo "нет ядра $CORE (соберите mobile/build-android.sh)"; exit 1; }

echo "== ресурсы и R.java"
"$AAPT2" compile --dir "$APP/res" -o "$WORK/res.zip"
# aapt2 требует package в манифесте; в исходнике он приходит из build.gradle.
python3 - "$APP/AndroidManifest.xml" "$WORK/AndroidManifest.xml" <<'PY'
import sys
src, dst = sys.argv[1], sys.argv[2]
s = open(src).read()
s = s.replace('<manifest xmlns:android="http://schemas.android.com/apk/res/android">',
              '<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="ru.usatyi.vpn">', 1)
open(dst, 'w').write(s)
PY
"$AAPT2" link -o "$WORK/base.apk" \
	-I "$TOOLS/android.jar" --manifest "$WORK/AndroidManifest.xml" \
	--java "$WORK/gen" "$WORK/res.zip" \
	--min-sdk-version 28 --target-sdk-version 34 --version-code 13 --version-name 1.3.4

echo "== R.java (ECJ: javac не нужен)"
mkdir -p "$WORK/rjava"
"$JAVA" -jar "$TOOLS/ecj-3.45.0.jar" -source 8 -target 8 \
	-bootclasspath "$TOOLS/android.jar" -d "$WORK/rjava" "$(find "$WORK/gen" -name '*.java')" 2>&1 |
	grep -v 'warning:\|^Note:' || true

echo "== Kotlin"
python3 "$ROOT/tools/gen_bindings.py" >/dev/null
(cd "$APP/java" && find "$PWD/ru" -name '*.kt' > "$WORK/kt.txt")
"$KOTLINC" -nowarn -classpath "$TOOLS/android.jar:$JSCH:$WORK/rjava" \
	-d "$WORK/classes" @"$WORK/kt.txt" 2>&1 | grep -v '^warning:' || true
test -d "$WORK/classes" || { echo "Kotlin не собрался"; exit 1; }

echo "== dex"
# JSch тянет необязательный jzlib (сжатие SSH). Мы его не используем, а пакета нет —
# поэтому класс сжатия вырезаем, иначе в dex остаётся «висячая» ссылка на jzlib.
mkdir -p "$WORK/jsch"
python3 - "$JSCH" "$WORK/jsch/jsch-min.jar" <<'PY'
import sys, zipfile
src, dst = sys.argv[1], sys.argv[2]
zi = zipfile.ZipFile(src)
zo = zipfile.ZipFile(dst, 'w', zipfile.ZIP_DEFLATED)
for it in zi.infolist():
    if it.filename == 'com/jcraft/jsch/jcraft/Compression.class':
        continue
    zo.writestr(it, zi.read(it.filename))
zo.close()
PY
JSCH_MIN="$WORK/jsch/jsch-min.jar"
mkdir -p "$WORK/dex"
"$JAVA" -cp "$TOOLS/d8.jar" com.android.tools.r8.D8 --release --min-api 28 \
	--lib "$TOOLS/android.jar" --output "$WORK/dex" \
	$(find "$WORK/classes" -name '*.class') "$KOTLIN_STDLIB" "$JSCH_MIN" 2>&1 |
	grep -v 'Info:\|Warning:\|malformed kotlin.Metadata\|error occurred when parsing kotlin metadata\|Unexpected error during rewriting\|Should never be called\|^\s*at ' || true
test -f "$WORK/dex/classes.dex" || { echo "d8 не сделал classes.dex"; exit 1; }

echo "== упаковка"
python3 - "$WORK/base.apk" "$WORK/dex/classes.dex" "$APP" "$WORK/unaligned.apk" <<'PY'
import os, shutil, sys, zipfile
base, dex, app, out = sys.argv[1:5]
shutil.copy(base, out)
z = zipfile.ZipFile(out, 'a', zipfile.ZIP_DEFLATED)
z.write(dex, 'classes.dex')
core = os.path.join(app, 'jniLibs/arm64-v8a/libusatyi_core.so')
z.write(core, 'lib/arm64-v8a/libusatyi_core.so', zipfile.ZIP_STORED)
assets = os.path.join(app, 'assets')
for root, _, files in os.walk(assets):
    for f in files:
        p = os.path.join(root, f)
        z.write(p, 'assets/' + os.path.relpath(p, assets), zipfile.ZIP_DEFLATED)
z.close()
PY

echo "== проверка, что все нужные классы внутри"
python3 "$ROOT/tools/check-apk.py" "$WORK/dex/classes.dex" "$TOOLS/android.jar" 2>/dev/null

echo "== подпись"
mkdir -p "$(dirname "$OUT")"
"$JAVA" -jar "$TOOLS/apksigner.jar" sign --ks "$KS" --ks-key-alias "$KS_ALIAS" \
	--ks-pass "pass:$KS_PASS" --key-pass "pass:$KS_PASS" --min-sdk-version 28 \
	--out "$OUT" "$WORK/unaligned.apk" 2>&1 | grep -v WARNING || true
"$JAVA" -jar "$TOOLS/apksigner.jar" verify --min-sdk-version 28 "$OUT" 2>&1 | grep -v WARNING || true

echo "готово: $OUT"
ls -la "$OUT"
