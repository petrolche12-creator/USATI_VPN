//go:build !windows

package main

// Заглушка для проверок на Linux: без шифрования. В Windows-сборку не попадает.

func protectSecret(plain []byte) ([]byte, error) {
	return append([]byte{}, plain...), nil
}

func unprotectSecret(blob []byte) ([]byte, error) {
	return append([]byte{}, blob...), nil
}
