package main

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"strings"
)

// Встроенные файлы: серверные скрипты, список российских сетей и драйвер WinTun.
// Страница браузерного интерфейса (ui/) встроена только в Linux-сборке для проверок:
// в Windows-версии интерфейс нативный (gui_windows.go), и файлов браузера в exe нет.
//
//go:embed assets
var assetsFS embed.FS

// wintunDLL — драйвер WinTun 0.14.1 (лицензия WireGuard LLC, см. assets/WINTUN-LICENSE.txt).
var wintunDLL = mustAsset("assets/wintun.dll")

// notRUList — сети, которые в режиме «Кроме российских IP» идут через VPN: дополнение до всего IPv4
// без российских и локальных блоков (см. tools/make_routes.py). Остальное идёт напрямую.
var notRUList = loadNotRU()

func readAsset(name string) ([]byte, error) {
	b, err := assetsFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("в программе нет файла %s: %w", name, err)
	}
	return b, nil
}

func mustAsset(name string) []byte {
	b, err := readAsset(name)
	if err != nil {
		panic(err)
	}
	return b
}

func loadNotRU() []string {
	b, err := readAsset("assets/notru.txt")
	if err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}
