//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// protectSecret шифрует данные учётной записью Windows (DPAPI): читать их сможет только этот пользователь на этом компьютере.
func protectSecret(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}

// unprotectSecret расшифровывает данные, зашифрованные protectSecret.
func unprotectSecret(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	res := make([]byte, out.Size)
	copy(res, unsafe.Slice(out.Data, out.Size))
	return res, nil
}
