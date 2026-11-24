//go:build windows

package main

// AmneziaWG-туннель на встроенной библиотеке amneziawg-go: адаптер WinTun и сетевой стек netstack
// внутри процесса. Через этот туннель программа ходит по SSH на хост с MTP-VPN, а в режиме
// «Amnezia как VPN» (по умолчанию) — весь трафик компьютера, как в клиенте Amnezia.

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	awgconn "github.com/amnezia-vpn/amneziawg-go/v3/conn"
	awg "github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
)

func init() {
	awgStarter = startAwgWindows
}

// startAwgWindows поднимает AmneziaWG. routeAll — направить весь IPv4-трафик компьютера в туннель.
func startAwgWindows(c *AwgConfig, routeAll bool, logf func(string, ...any)) (*awgTunnel, error) {
	addr, mask, err := awgAddrMask(c)
	if err != nil {
		return nil, err
	}
	mtu := awgMtu(c)

	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, awgDns(c), mtu)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать адаптер AmneziaWG (нужны права администратора и драйвер WinTun): %w", err)
	}
	dev := awg.NewDevice(tunDev, awgconn.NewDefaultBind(), awgLogger(logf))

	t := &awgTunnel{
		stack:  &stackDialer{tnet: tnet},
		server: c.Peer.Endpoint,
		stats:  devStats(dev),
		stop: func() {
			_ = dev.Down()
			dev.Close()
		},
		logf: logf,
	}
	if err := dev.IpcSet(awgUAPI(c)); err != nil {
		t.Stop()
		return nil, fmt.Errorf("параметры AmneziaWG не приняты: %w", err)
	}
	if err := dev.Up(); err != nil {
		t.Stop()
		return nil, fmt.Errorf("не удалось поднять AmneziaWG: %w", err)
	}
	name, err := tunDev.Name()
	if err != nil {
		t.Stop()
		return nil, err
	}
	if err := netSetAddress(name, net.IP(addr.AsSlice()), mask); err != nil {
		t.Stop()
		return nil, err
	}
	if err := netSetMTU(name, mtu); err != nil {
		t.Stop()
		return nil, err
	}
	if dns := awgDns(c); len(dns) > 0 {
		if ip := net.ParseIP(dns[0].String()); ip != nil {
			_ = netSetDNS(name, ip.To4())
		}
	}
	if routeAll {
		if err := routeAllThroughAwg(name, c, logf); err != nil {
			t.Stop()
			return nil, err
		}
	}
	logf("AmneziaWG поднят: адаптер %s, адрес %s/%d, сервер %s, MTU %d", name, addr, mask, c.Peer.Endpoint, mtu)
	return t, nil
}

// awgLogger — журнал библиотеки AmneziaWG идёт в журнал программы.
func awgLogger(logf func(string, ...any)) *awg.Logger {
	l := &awg.Logger{Verbosef: func(string, ...any) {}, Errorf: func(string, ...any) {}}
	l.Errorf = func(format string, args ...any) { logf("amneziawg: "+format, args...) }
	return l
}

// devStats — счётчики трафика AmneziaWG по данным устройства.
func devStats(dev *awg.Device) func() (int64, int64) {
	return func() (int64, int64) {
		s, err := dev.IpcGet()
		if err != nil {
			return 0, 0
		}
		var rx, tx int64
		for _, line := range strings.Split(s, "\n") {
			switch {
			case strings.HasPrefix(line, "rx_bytes="):
				rx += parseI64(line[len("rx_bytes="):])
			case strings.HasPrefix(line, "tx_bytes="):
				tx += parseI64(line[len("tx_bytes="):])
			}
		}
		return rx, tx
	}
}

// parseI64 — число из строки UAPI (0 при ошибке).
func parseI64(s string) int64 {
	var n int64
	neg := false
	for i, r := range s {
		if i == 0 && r == '-' {
			neg = true
			continue
		}
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int64(r-'0')
	}
	if neg {
		n = -n
	}
	return n
}

// stackDialer — TCP через стек netstack внутри туннеля.
type stackDialer struct{ tnet *netstack.Net }

func (d *stackDialer) DialTCP(host string, port int) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var ip netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		ip = a
	} else {
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("не удалось найти адрес %s: %w", host, err)
		}
		for _, cand := range ips {
			if v4 := cand.To4(); v4 != nil {
				ip, _ = netip.ParseAddr(v4.String())
				break
			}
		}
		if !ip.IsValid() {
			return nil, fmt.Errorf("у сервера %s нет IPv4-адреса", host)
		}
	}
	return d.tnet.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(ip, uint16(port)))
}

// routeAllThroughAwg отправляет весь IPv4-трафик компьютера в адаптер AmneziaWG и перекрывает DNS,
// чтобы браузер и все программы шли через Amnezia. Аварийное восстановление DNS и IPv6 —
// recoverPrivacy при следующем запуске (файл net-restore.json).
func routeAllThroughAwg(name string, c *AwgConfig, logf func(string, ...any)) error {
	gw, err := netDefaultGateway()
	if err != nil {
		return err
	}
	// Сам трафик до сервера AmneziaWG не должен уходить в туннель: маршрут через физический шлюз.
	if ip, err := resolveIPv4(c.Host()); err == nil && !ip.IsLoopback() {
		if err := netAddHostRoute(ip, gw); err != nil {
			return err
		}
	}
	if err := netAddRoutes(name, net.IPv4zero, []string{"0.0.0.0/1", "128.0.0.0/1"}); err != nil {
		return err
	}
	dns := net.IPv4(1, 1, 1, 1)
	if list := awgDns(c); len(list) > 0 {
		if ip := net.ParseIP(list[0].String()); ip != nil {
			dns = ip.To4()
		}
	}
	// DNS на физических адаптерах перекрываем на адрес из Amnezia: резолв идёт внутрь туннеля.
	tunUpPrivacy(defaultDataDir(), logf, dns, ModeNormal, c.Host())
	return nil
}
