//go:build !windows

package main

func hasConsole() bool { return false }
func consolePause()    {}
