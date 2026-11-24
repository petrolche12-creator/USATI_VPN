# Готовые сборки

## Windows (.exe)

```sh
cd usatish && GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o ../dist/UsatyiVPN.exe .
```

В программе: вкладка «Подключение» → кнопка **«Загрузить .conf Amnezia…»**. Файл .conf
берётся в приложении Amnezia: сервер → «Поделиться» → «Файл .conf». После загрузки программа
подключается сначала к серверу Amnezia по этому файлу (AmneziaWG), затем по SSH и уже через
этот канал открывает MTP-VPN.

## Android (.apk)

APK собирается в GitHub Actions. Один раз скопируйте заготовку в нужное место:

```sh
mkdir -p .github/workflows && cp tools/ci/android.yml .github/workflows/android.yml
git add .github && git commit -m "CI: сборка APK" && git push
```

Дальше: **Actions → «Android APK» → Run workflow**.
В артефакте `usatyi-vpn-apk` будет подписанный APK.

Локальная сборка (нужны Go, JDK 17, Android SDK):

```sh
sh mobile/build-android.sh                      # ядро цепочки под arm64
cd android && ./gradlew assembleRelease         # APK
```

В приложении на вкладке «Подключиться» — блок **Amnezia** с кнопкой **«Добавить Amnezia»**:
выбираете .conf, дальше «Подключить» поднимает AmneziaWG → SSH → MTP.

Подробности о ядре и почему оно в отдельном процессе — `mobile/README.md`.
