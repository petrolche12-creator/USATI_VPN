//go:build windows

package main

// На Windows привязки сокетов к адаптеру нет: трафик к серверу идёт по обычным маршрутам.
func interfaceIndex(name string) int { return 0 }
