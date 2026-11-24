# Готовые сборки

Оба файла собраны без CI — их можно просто скачать и запустить.
Подпись/ключи и параметры сборки: `.exe` — Go, `.apk` — `tools/build-apk.sh`.

## Windows (.exe)

`dist/UsatyiVPN.exe` — обычная программа для Windows 10/11 x64, установка не нужна.

Пересобрать:

```sh
cd usatish && GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o ../dist/UsatyiVPN.exe .
```

В программе: вкладка «Подключение» → кнопка **«Загрузить .conf Amnezia…»**. Файл .conf
берётся в приложении Amnezia: сервер → «Поделиться» → «Файл .conf». После загрузки программа
подключается сначала к серверу Amnezia по этому файлу (AmneziaWG), затем по SSH и уже через
этот канал открывает MTP-VPN.

## Android (.apk)

`dist/UsatyiVPN.apk` — подписанный APK, ставится на телефон без Google Play.

- Только **arm64** (все телефоны с Android 9+; x86-эмулятор не подойдёт — там нет ядра цепочки).
- Android **9 (API 28) и новее**.
- Подпись: `android/app/usatyi-release.jks`, alias `usatyi` (v3-схема, для установки сбоку хватает).
- SHA-256: `6afe1a9b5d943145351199375be4578fa8a1a25191cfa193ea212fad6d7a66b2`.

Установка: скинуть файл на телефон → открыть → разрешить «Установка из неизвестных источников».
Или по кабелю: `adb install -r dist/UsatyiVPN.apk`.

В приложении на вкладке «Подключиться» — блок **Amnezia** с кнопкой **«Добавить Amnezia»**:
выбираете .conf, дальше «Подключить» поднимает AmneziaWG → SSH → MTP.

### Как собран APK (без Gradle и Android SDK)

```sh
sh tools/build-apk.sh dist/UsatyiVPN.apk
```

Скрипт делает всё сам: `mobile/build-android.sh` → `libusatyi_core.so`, `aapt2` → ресурсы и
`R.java`, `ecj` → `R.class`, `kotlinc` → Kotlin, `d8` → `classes.dex`, `apksigner` → подпись.
Нужные переменные: `TOOLS`, `AAPT2`, `KOTLINC`, `JSCH`, `JAVA` (подробности — в шапке скрипта).

Почему не Gradle: Gradle тянет AndroidX и Material из Maven, а без сети до Maven сборка
невозможна. Поэтому приложение переведено на системные виджеты (`android:Theme.Material`,
свои `CardView`/`BottomBar`/`ProgressLine` в `Compat.kt`), а `ViewBinding`-классы генерирует
`tools/gen_bindings.py`. Функции те же, внешних зависимостей нет.

Что в этой сборке отличается: **прямой режим WireGuard отключён** (нужна библиотека
`com.wireguard.android:tunnel`, её нет без Maven). Цепочка через загруженный .conf
(AmneziaWG → SSH → MTP) работает полностью.

Подробности о ядре и почему оно в отдельном процессе — `mobile/README.md`.
