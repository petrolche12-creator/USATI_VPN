//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// wintunOnce: драйвер выкладывается на диск и загружается один раз, и только когда
// пользователь реально подключается. На старте программа ничего не пишет рядом с собой —
// так её реже принимают за вредоносную и антивирусы не убивают процесс при запуске.
var wintunOnce sync.Once
var wintunErrOnce error

func ensureWintun(dir string) error {
	wintunOnce.Do(func() { wintunErrOnce = prepareWintun(dir) })
	return wintunErrOnce
}

// prepareWintun кладёт драйвер WinTun (встроен в программу) в каталог данных и загружает его.
// Библиотека WireGuard ищет wintun.dll в каталоге программы, поэтому загружаем его по полному пути
// (тогда поиск по имени находит уже загруженный модуль) и, если можно, держим копию рядом с программой.
func prepareWintun(dir string) error {
	dll := filepath.Join(dir, "wintun.dll")
	cur, err := os.ReadFile(dll)
	if err != nil || !bytes.Equal(cur, wintunDLL) {
		if err := os.WriteFile(dll, wintunDLL, 0o644); err != nil {
			return err
		}
	}
	if exe, err := os.Executable(); err == nil {
		side := filepath.Join(filepath.Dir(exe), "wintun.dll")
		if cur, err := os.ReadFile(side); err != nil || !bytes.Equal(cur, wintunDLL) {
			_ = os.WriteFile(side, wintunDLL, 0o644)
		}
	}
	_, err = windows.LoadLibrary(dll)
	return err
}
