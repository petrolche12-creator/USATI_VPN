//go:build !windows

package main

// На Linux драйвер WinTun не нужен: туннель идёт через /dev/net/tun (только для проверок).

func prepareWintun(dir string) error { return nil }
