//go:build !windows

package main

// Запуск на Linux — только для проверок разработчиком: локальный HTTP-интерфейс и браузер.
// В Windows-версии (main_windows.go) этого нет: там нативное окно Win32, и программа
// не слушает порты вообще. Туннели, ключи, сервер и настройки — общий код (app.go и др.).

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// openCheckAfter — через сколько секунд после открытия окна сообщить, что интерфейс не отозвался.
const openCheckAfter = 30 * time.Second

func main() {
	dirFlag := flag.String("dir", "", "каталог данных")
	portFlag := flag.Int("port", 0, "порт интерфейса на 127.0.0.1 (0 — случайный)")
	noWindow := flag.Bool("no-window", false, "не открывать окно, вывести адрес в консоль")
	flag.Parse()

	dir := *dirFlag
	if dir == "" {
		dir = defaultDataDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fatal(dir, err)
	}
	logf := newLogger(dir)
	logf("запуск")

	if err := prepareWintun(dir); err != nil {
		logf("драйвер WinTun не загрузился: %v", err)
	}
	app, err := NewApp(dir, logf)
	if err != nil {
		fatal(dir, err)
	}

	token, err := randomToken()
	if err != nil {
		fatal(dir, err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *portFlag))
	if err != nil {
		fatal(dir, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: app.Handler(token, port), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, token)
	if *noWindow {
		fmt.Println(url)
	} else if err := openWindow(url, dir); err != nil {
		logf("окно не открылось (%v). Откройте в браузере: %s", err, url)
		go showMessage("Усатый VPN", openHelp(url, dir), false)
	} else {
		go checkOpened(app, logf, url, dir)
	}

	app.Wait()
	app.Shutdown()
	_ = srv.Close()
	logf("выход")
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// checkOpened: если интерфейс не отозвался за openCheckAfter, показываем адрес и путь к журналу.
func checkOpened(app *App, logf func(string, ...any), url, dir string) {
	time.Sleep(openCheckAfter)
	if app.Opened() {
		return
	}
	logf("интерфейс не отозвался за %v: %s", openCheckAfter, url)
	showMessage("Усатый VPN", openHelp(url, dir), false)
}

// openHelp — что сделать, если окно программы не открылось.
func openHelp(url, dir string) string {
	return "Окно программы не открылось.\n\nОткройте адрес в браузере вручную:\n" + url +
		"\n\nЕсли не помогло, пришлите файл журнала:\n" + filepath.Join(dir, "app.log")
}
