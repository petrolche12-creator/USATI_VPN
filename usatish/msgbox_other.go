//go:build !windows

package main

import (
	"fmt"
	"os"
)

// showMessage на Linux (для проверок): пишет сообщение в поток ошибок.
func showMessage(title, text string, isError bool) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, text)
}
