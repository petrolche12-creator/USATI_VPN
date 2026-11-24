# Мобильное ядро цепочки (Android)

Здесь живёт то, что на Android делает всю сетевую работу:

```
[.conf Amnezia] → [AmneziaWG] → [SSH на свой сервер] → [MTP-VPN]
```

Пакеты из системного VPN (Kotlin) приходят в ядро, ядро отдаёт их обратно — Kotlin только
читает и пишет файл интерфейса. Никакой сетевой логики в Kotlin нет.

## Почему отдельный процесс

`amneziawg-go` (v3) поднимает туннель в пользовательском пространстве (gvisor/netstack) и сам
открывает UDP-сокеты к серверу Amnezia. Если это делать внутри процесса, который держит
`VpnService`, Android затянет эти сокеты в собственный туннель — получится петля.

Поэтому:

* ядро — отдельный исполняемый файл (`mobile/daemon`, в APK лежит как
  `jniLibs/arm64-v8a/libusatyi_core.so`);
* Kotlin-служба `ChainService` помечает **своё приложение** исключённым из VPN
  (`addDisallowedApplication`), и весь трафик ядра идёт мимо туннеля;
* сама служба поднимает `VpnService` поверх MTP-адреса, который сообщило ядро.

Пакеты между ними идут по локальному сокету Android (abstract namespace), формат кадров —
в `core/ipc.go` и `android/.../CoreProcess.kt`.

## Сборка

```sh
# ядро под телефон (arm64): попадёт в android/app/src/main/jniLibs
sh mobile/build-android.sh

# APK — обычный Gradle (нужны JDK 17 и Android SDK):
cd android && ./gradlew assembleRelease
```

Готовый APK собирает и CI: `.github/workflows/android.yml` (кладёт артефакт в запуск).

## Тесты

```sh
cd mobile && go test -race ./...
```

* разбор `.conf` Amnezia и ключей `mtp://`;
* протокол обмена пакетами (рукопожатие, пакеты, статус, счётчики, остановка) — `ipc_test.go`;
* SSH через подменённый транспорт и проверка отпечатка ключа сервера — `ssh_test.go`.

## Ограничения

* Только arm64: Go под Android требует cgo для 32-битных и x86 ABI, а NDK в CI нет
  (телефоны с Android 9+ практически все 64-битные).
* Через SSH не проходит UDP, поэтому MTP/1 (UDP) в цепочке подменяется на MTP/2 — ядро
  предупреждает об этом в журнале.
