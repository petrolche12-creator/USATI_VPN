//go:build !windows

package main

// На Linux (проверки на машине разработчика) приватность системы не переключается.

import "net"

func tunUpPrivacy(dir string, logf func(string, ...any), dns net.IP, mode Mode, host string) {}
func tunDownPrivacy(logf func(string, ...any))                                               {}
func recoverPrivacy(dir string, logf func(string, ...any))                                   {}
