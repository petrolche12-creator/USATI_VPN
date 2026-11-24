#!/bin/sh
# Сборка ядра для Android: бинарник daemon под каждую ABI, который Gradle кладёт в APK
# как нативную библиотеку libusatyi_core.so (так систему проще запустить файл).
#
#   sh mobile/build-android.sh            # собрать в android/app/src/main/jniLibs
#   OUT=/tmp/libs sh mobile/build-android.sh
#
# Ядро собирается без CGO: amneziawg-go v3 работает на чистом Go (gvisor/netstack).
set -eu

cd "$(dirname "$0")"
OUT="${OUT:-../android/app/src/main/jniLibs}"

export CGO_ENABLED=0
export GOOS=android
export GOFLAGS="-mod=mod -trimpath"

# ABI → GOARCH. Только arm64: Go под Android требует cgo для arm/386/amd64
# (32-битные и x86 — это только эмулятор), поэтому одна ABI: телефоны с Android 9+ и 64 битами.
for pair in arm64-v8a:arm64; do
	abi="${pair%%:*}"
	arch="${pair##*:}"
	dir="$OUT/$abi"
	mkdir -p "$dir"
	echo "== $abi ($arch)"
	GOARCH="$arch" go build -ldflags "-s -w" -o "$dir/libusatyi_core.so" ./daemon
	chmod 755 "$dir/libusatyi_core.so"
done

echo "готово: $OUT"
ls -la "$OUT"/*/libusatyi_core.so
