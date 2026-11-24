//go:build !windows

package main

// Linux-реализация — только для проверок на машине разработчика (TUN в сетевом пространстве).
// В Windows-сборку этот файл не попадает.

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

func runNet(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func netDefaultGateway() (net.IP, error) {
	out, err := runNet("ip", "-4", "route", "show", "default")
	if err != nil {
		return nil, err
	}
	f := strings.Fields(out)
	for i := range f {
		if f[i] == "via" && i+1 < len(f) {
			if ip := net.ParseIP(f[i+1]); ip != nil {
				return ip.To4(), nil
			}
		}
	}
	return nil, fmt.Errorf("нет маршрута по умолчанию")
}

func netSetAddress(name string, ip net.IP, prefix int) error {
	_, err := runNet("ip", "addr", "replace", fmt.Sprintf("%s/%d", ip, prefix), "dev", name)
	return err
}

func netSetMTU(name string, mtu int) error {
	_, err := runNet("ip", "link", "set", "dev", name, "mtu", strconv.Itoa(mtu), "up")
	return err
}

func netSetDNS(name string, dns net.IP) error { return nil }

func netAddHostRoute(dst, via net.IP) error {
	if dst.IsLoopback() || via == nil {
		return nil
	}
	_, err := runNet("ip", "route", "replace", dst.String()+"/32", "via", via.String())
	return err
}

func netDelHostRoute(dst net.IP) {
	if dst.IsLoopback() {
		return
	}
	_, _ = runNet("ip", "route", "del", dst.String()+"/32")
}

func netAddRoutes(name string, via net.IP, cidrs []string) error {
	for _, c := range cidrs {
		if strings.Contains(c, ":") {
			continue
		}
		if _, err := runNet("ip", "route", "replace", c, "dev", name); err != nil {
			return err
		}
	}
	return nil
}

func netRemoveRoutesFast(name string, via net.IP, cidrs []string) {}
