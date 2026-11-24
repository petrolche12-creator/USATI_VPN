//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

// runNet запускает системную утилиту без окна консоли.
func runNet(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// netDefaultGateway — шлюз физической сети (маршрут 0.0.0.0 с наименьшей метрикой).
func netDefaultGateway() (net.IP, error) {
	out, err := runNet("route", "print", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	var best net.IP
	bestMetric := int(^uint(0) >> 1)
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue
		}
		gw := net.ParseIP(f[2])
		if gw == nil || gw.To4() == nil {
			continue
		}
		metric, err := strconv.Atoi(f[4])
		if err != nil {
			continue
		}
		if metric < bestMetric {
			bestMetric = metric
			best = gw.To4()
		}
	}
	if best == nil {
		return nil, errors.New("не нашёл шлюз по умолчанию: проверьте подключение к интернету")
	}
	return best, nil
}

// netSetAddress задаёт адрес адаптеру.
func netSetAddress(name string, ip net.IP, prefix int) error {
	mask := net.IP(net.CIDRMask(prefix, 32)).String()
	_, err := runNet("netsh", "interface", "ip", "set", "address", "name="+name, "static", ip.String(), mask)
	return err
}

// netSetMTU задаёт MTU адаптеру.
func netSetMTU(name string, mtu int) error {
	_, err := runNet("netsh", "interface", "ipv4", "set", "subinterface", name, "mtu="+strconv.Itoa(mtu), "store=active")
	return err
}

// netSetDNS задаёт DNS-сервер адаптеру.
func netSetDNS(name string, dns net.IP) error {
	if dns == nil {
		return nil
	}
	_, err := runNet("netsh", "interface", "ip", "set", "dns", "name="+name, "static", dns.String(), "primary")
	return err
}

// netAddRoutes направляет сети в адаптер. Список «кроме российских IP» — это тысячи
// префиксов, поэтому маршруты добавляются системным API iphlpapi прямо из процесса:
// те же тысячи записей за секунды, без зависания окна. netsh — запасной путь.
func netAddRoutes(name string, via net.IP, cidrs []string) error {
	var v4 []string
	for _, c := range cidrs {
		if strings.Contains(c, ":") {
			continue
		}
		v4 = append(v4, c)
	}
	if len(v4) == 0 {
		return nil
	}
	if err := netAddRoutesFast(name, via, v4); err == nil {
		return nil
	}
	return netAddRoutesNetsh(v4, name, via)
}

var (
	modIphlpapi         = windows.NewLazySystemDLL("iphlpapi.dll")
	pCreateIpForwardRow = modIphlpapi.NewProc("CreateIpForwardEntry")
)

// ipForwardRow — MIB_IPFORWARDROW из iphlpapi.h (все поля — DWORD).
type ipForwardRow struct {
	dest, mask, policy, nextHop, ifIndex uint32
	typ, proto, age, nextHopAS           uint32
	metric1, metric2, metric3            uint32
	metric4, metric5                     uint32
}

// ip4u32 пакует IPv4 в DWORD так, как ждёт iphlpapi (порядок байтов inet_addr).
func ip4u32(ip net.IP) uint32 {
	b := ip.To4()
	if b == nil {
		return 0
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// adapterIndex возвращает индекс интерфейса по имени (например, туннеля).
func adapterIndex(name string) (uint32, error) {
	var size uint32
	_ = windows.GetAdaptersAddresses(windows.AF_INET, 0, 0, nil, &size)
	if size == 0 {
		return 0, errors.New("система не вернула список адаптеров")
	}
	buf := make([]byte, size)
	aas := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
	if err := windows.GetAdaptersAddresses(windows.AF_INET, 0, 0, aas, &size); err != nil {
		return 0, err
	}
	want := strings.ToLower(name)
	for a := aas; a != nil; a = a.Next {
		if strings.ToLower(windows.UTF16PtrToString(a.FriendlyName)) == want {
			return a.IfIndex, nil
		}
	}
	return 0, errors.New("адаптер не найден: " + name)
}

// netAddRoutesFast добавляет маршруты системным вызовом, по записи на префикс.
func netAddRoutesFast(name string, via net.IP, cidrs []string) error {
	if pCreateIpForwardRow.Find() != nil {
		return errors.New("нет CreateIpForwardEntry")
	}
	idx, err := adapterIndex(name)
	if err != nil {
		return err
	}
	hop := ip4u32(via)
	added, failed := 0, 0
	for _, c := range cidrs {
		ip, net4, err := net.ParseCIDR(c)
		if err != nil || ip.To4() == nil {
			failed++
			continue
		}
		row := ipForwardRow{
			dest: ip4u32(net4.IP), mask: ip4u32(net.IP(net4.Mask)),
			nextHop: hop, ifIndex: idx,
			typ: 4, proto: 3, metric1: 5, // indirect, netmgmt
		}
		r, _, _ := pCreateIpForwardRow.Call(uintptr(unsafe.Pointer(&row)))
		if r == 0 || r == 0x1392 { // ERROR_OBJECT_ALREADY_EXISTS
			added++
		} else {
			failed++
		}
	}
	if added == 0 && failed > 0 {
		return fmt.Errorf("система не добавила ни одного маршрута из %d", failed)
	}
	return nil
}

// netAddRoutesNetsh — запасной путь: батч-файл для netsh (медленно, но везде работает).
func netAddRoutesNetsh(v4 []string, name string, via net.IP) error {
	f, err := os.CreateTemp("", "usatyi-routes-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	w := bufio.NewWriter(f)
	for _, c := range v4 {
		fmt.Fprintf(w, "interface ipv4 add route prefix=%s interface=%s nexthop=%s metric=5 store=active\n", c, name, via.String())
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	_, err = runNet("netsh", "-f", f.Name())
	return err
}

// netRemoveRoutesFast удаляет маршруты, которые ставила программа (для отключения).
func netRemoveRoutesFast(name string, via net.IP, cidrs []string) {
	idx, err := adapterIndex(name)
	if err != nil {
		return
	}
	pDelete := modIphlpapi.NewProc("DeleteIpForwardEntry")
	if pDelete.Find() != nil {
		return
	}
	hop := ip4u32(via)
	for _, c := range cidrs {
		ip, net4, err := net.ParseCIDR(c)
		if err != nil || ip.To4() == nil {
			continue
		}
		row := ipForwardRow{dest: ip4u32(net4.IP), mask: ip4u32(net.IP(net4.Mask)), nextHop: hop, ifIndex: idx}
		_, _, _ = pDelete.Call(uintptr(unsafe.Pointer(&row)))
	}
}

// netAddHostRoute — маршрут до сервера через физический шлюз: трафик к серверу не уходит в туннель.
func netAddHostRoute(dst, via net.IP) error {
	_, _ = runNet("route", "delete", dst.String())
	_, err := runNet("route", "add", dst.String(), "mask", "255.255.255.255", via.String(), "metric", "1")
	return err
}

// netDelHostRoute удаляет маршрут до сервера.
func netDelHostRoute(dst net.IP) {
	_, _ = runNet("route", "delete", dst.String())
}
