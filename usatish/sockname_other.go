//go:build !windows

package main

import (
	"net"
	"syscall"
)

// interfaceIndex — индекс сетевого интерфейса по имени (для SO_BINDTODEVICE в тестах на Linux).
// В Windows не нужно: там программы ходят к серверу через настоящий адаптер с маршрутами.
func interfaceIndex(name string) int {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return 0
	}
	return ifi.Index
}

var _ = syscall.SO_BINDTODEVICE
