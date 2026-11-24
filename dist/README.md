# Готовые сборки

Оба файла собраны без CI — их можно просто скачать и запустить.
Подпись/ключи и параметры сборки: `.exe` — Go, `.apk` — `tools/build-apk.sh`.

## Windows (.exe)

`dist/UsatyiVPN.exe` — обычная программа для Windows 10/11 x64, установка не нужна.
Версия 1.3.8, SHA-256: `b5852464b83e47afe5f38614f21ee9c37e37bd50aea2005d1411ac4489ccd542`.

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
- Версия 1.3.2 (versionCode 11), SHA-256: `9bc80e6e676c1401a71b1965cb7af4df9768355d425ec34d39b385eb5197cccc`.

Установка: скинуть файл на телефон → открыть → разрешить «Установка из неизвестных источников».
Или по кабелю: `adb install -r dist/UsatyiVPN.apk`.

В приложении на вкладке «Подключиться» — блок **Amnezia** с кнопкой **«Добавить Amnezia»**:
выбираете .conf, дальше «Подключить» поднимает AmneziaWG → SSH → MTP.

### Что исправлено в 1.3.2 (ядро падало при подключении)

Ядро (`libusatyi_core.so`) падало в `main.main()` с `panic: close of closed channel`:
`amneziawg-go` при `dev.Close()` сам закрывает tun-устройство, а код закрывал его
второй раз. На телефоне это выглядело как «Ядро завершилось при запуске» — подключиться
было нельзя вообще.

Теперь:
1. tun-устройство закрывается один раз (фикс в `mobile/core/awg_mobile.go`);
2. паника в ядре больше не убивает процесс: она превращается в понятную ошибку
   в журнале (`core.log`), а цикл обмена пакетами защищён `recover`;
3. есть регрессия-тест `TestAWGStopIsSafeOnceAndManyTimes` — без фикса он падает.

В `.exe` двойного закрытия не было, но он пересобран с тем же ядром и обновлён.

### Если приложение не открывается

Начиная с версии 1.3.1 приложение при сбое не закрывается молча: показывается аварийный
экран с текстом ошибки и кнопкой «Скопировать ошибку» — пришлите этот текст, если что-то
не так.

Что было исправлено в 1.3.1 (предыдущая сборка падала при запуске):

1. в APK не попадала **kotlin-stdlib** — приложение падало с `NoClassDefFoundError:
   kotlin/jvm/internal/Intrinsics` сразу при старте. Теперь stdlib кладётся в `classes.dex`
   всегда, а `tools/check-apk.py` проверяет это перед подписью;
2. у своих виджетов (`CardView`, `BottomBar`, `ProgressLine`) не было конструктора
   `(Context, AttributeSet)` — `LayoutInflater` падал с `InstantiationException` при
   показе экрана. Добавлены `@JvmOverloads`-конструкторы, проверка конструкторов тоже
   встроена в `check-apk.py`;
3. из JSch убран необязательный класс сжатия (`jzlib`), из-за которого оставалась
   «висячая» ссылка;
4. `onCreate` обёрнут в `try/catch` с аварийным экраном.

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
