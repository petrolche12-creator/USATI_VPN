//go:build windows

package main

import "golang.org/x/sys/windows"

// Значения флагов MessageBox (user32).
const (
	mbIconError       = 0x00000010
	mbIconInformation = 0x00000040
	mbSetForeground   = 0x00010000
	mbTopmost         = 0x00040000
)

// showMessage показывает окно с текстом. У программы нет окна консоли, поэтому так пользователь
// узнаёт о причине, если программа не запустилась или интерфейс не открылся.
func showMessage(title, text string, isError bool) {
	t, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	x, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	icon := uint32(mbIconInformation)
	if isError {
		icon = mbIconError
	}
	_, _ = windows.MessageBox(0, x, t, icon|mbSetForeground|mbTopmost)
}
