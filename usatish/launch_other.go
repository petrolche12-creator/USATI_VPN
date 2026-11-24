//go:build !windows

package main

import "os/exec"

// openWindow на Linux (для проверок): открывает ссылку через xdg-open.
func openWindow(url string, dataDir string) error {
	return exec.Command("xdg-open", url).Start()
}
