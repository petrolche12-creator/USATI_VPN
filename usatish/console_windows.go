//go:build windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// hasConsole сообщает, есть у процесса консоль (отладочная сборка usatyi-vpn-debug.exe).
func hasConsole() bool {
	h, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(h, &mode) == nil
}

// consolePause держит окно консоли открытым, чтобы пользователь успел прочитать текст.
func consolePause() {
	if !hasConsole() {
		return
	}
	fmt.Println("Нажмите Enter, чтобы закрыть…")
	_, _ = os.Stdin.Read(make([]byte, 8))
}
