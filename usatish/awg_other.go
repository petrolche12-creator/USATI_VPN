//go:build !windows

package main

// Linux-реализация AmneziaWG — только для проверок цепочки на машине разработчика
// (TUN в отдельном сетевом пространстве). В Windows-сборку этот файл не попадает.

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
	awgStarter = startAwgLinux
}

// startAwgLinux поднимает AmneziaWG-адаптер в текущем сетевом пространстве (нужен root).
func startAwgLinux(c *AwgConfig, routeAll bool, logf func(string, ...any)) (*awgTunnel, error) {
	addr, mask, err := awgAddrMask(c)
	if err != nil {
		return nil, err
	}
	mtu := awgMtu(c)
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, awgDns(c), mtu)
	if err != nil {
		return nil, err
	}
	dev := awg.NewDevice(tunDev, awgconn.NewDefaultBind(), awg.NewLogger(awg.LogLevelSilent, ""))
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
		return nil, err
	}
	if err := dev.Up(); err != nil {
		t.Stop()
		return nil, err
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
	if routeAll {
		if gw, err := netDefaultGateway(); err == nil {
			_ = netAddRoutes(name, gw, []string{"0.0.0.0/1", "128.0.0.0/1"})
		}
	}
	logf("AmneziaWG поднят (Linux, проверка): %s/%d, сервер %s", addr, mask, c.Peer.Endpoint)
	return t, nil
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
			return nil, err
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

// awgLogger — журнал библиотеки в журнал программы.
func awgLogger(logf func(string, ...any)) *awg.Logger {
	l := &awg.Logger{}
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
