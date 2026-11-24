//go:build windows

package main

// Повышение прав только когда оно действительно нужно (подключение VPN).
// Окно программы открывается без запроса UAC: программа обязана запуститься
// и показать себя даже там, где повышение прав невозможно или запрещено.

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var pShellExecuteW = modUser32.NewProc("ShellExecuteW")

// isAdmin сообщает, запущен ли процесс с правами администратора.
func isAdmin() bool {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
		return false
	}
	defer tok.Close()
	return tok.IsElevated()
}

// relaunchElevated перезапускает программу от имени администратора: Windows покажет
// свой обычный запрос «Да/Нет». connectID непустой, если новая копия должна сразу
// подключиться этим ключом. Возвращает true, если повышенная копия запущена.
func relaunchElevated(dir, connectID string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	var args []string
	if dir != "" {
		args = append(args, "-dir", quoteArg(dir))
	}
	if connectID != "" {
		args = append(args, "-connect", quoteArg(connectID))
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	var params uintptr
	if len(args) > 0 {
		p, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
		params = uintptr(unsafe.Pointer(p))
	}
	r, _, _ := pShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		params, 0, 1) // SW_NORMAL
	return r > 32
}

func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + s + `"`
	}
	return s
}
