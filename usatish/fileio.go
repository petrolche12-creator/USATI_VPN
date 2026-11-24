package main

import "os"

// writeFileMode пишет файл атомарно (через временный) с заданными правами.
func writeFileMode(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
