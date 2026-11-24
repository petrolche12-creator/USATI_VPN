//go:build windows

package main

// Выбор файла через стандартное окно Windows (comdlg32, GetOpenFileNameW).
// Своё окно не нужно: проводник, «недавние места», сетевые диски — всё как везде в Windows.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ofnOverwritePrompt = 0x00000002
	ofnPathMustExist   = 0x00000800
	ofnFileMustExist   = 0x00001000
	ofnNoChangeDir     = 0x00000008
)

type openFileName struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	Flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	FlagsEx           uint32
}

var (
	modComdlg32    = windows.NewLazySystemDLL("comdlg32.dll")
	pGetOpenFileNm = modComdlg32.NewProc("GetOpenFileNameW")
)

// openConfDialog показывает диалог выбора файла .conf и возвращает путь (пустая строка — отмена).
func openConfDialog(owner uintptr) string {
	// Фильтр: «Конфиг Amnezia (*.conf)» и «Все файлы (*.*)»; двойной \x00 — конец списка.
	filter, _ := windows.UTF16FromString("Конфиг Amnezia (*.conf)\x00*.conf\x00Все файлы (*.*)\x00*.*\x00")
	title, _ := windows.UTF16FromString("Файл .conf из Amnezia («Поделиться»)")
	buf := make([]uint16, 4096)
	ofn := openFileName{
		hwndOwner:   owner,
		lpstrFilter: &filter[0],
		lpstrFile:   &buf[0],
		nMaxFile:    uint32(len(buf)),
		lpstrTitle:  &title[0],
		Flags:       ofnPathMustExist | ofnFileMustExist | ofnNoChangeDir,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	r, _, _ := pGetOpenFileNm.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
