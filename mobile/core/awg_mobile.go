//go:build linux || android

package core

// Мобильное ядро AmneziaWG: туннель поднимается в стеке пользовательского пространства
// (netstack), TCP для SSH и MTP идёт внутри стека. Файловые дескрипторы приложению не нужны.

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	awgconn "github.com/amnezia-vpn/amneziawg-go/v3/conn"
	awg "github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
)

// netDialer — то, что нужно цепочке от туннеля (для тестов подменяется).
type netDialer interface {
	DialTCP(host string, port int) (net.Conn, error)
}

// awgTunnel — поднятый AmneziaWG. Сокеты наружу идут через его стек, поэтому SSH живёт
// внутри туннеля независимо от системных маршрутов телефона.
type awgTunnel struct {
	stack  netDialer
	server string
	stats  func() (rx, tx int64)
	stop   func()
	once   sync.Once
	logf   func(string, ...any)
}

func (t *awgTunnel) Stop() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.stop != nil {
			t.stop()
		}
	})
}

func (t *awgTunnel) DialTCP(host string, port int) (net.Conn, error) {
	return t.stack.DialTCP(host, port)
}

func (t *awgTunnel) Stats() (rx, tx int64) {
	if t == nil || t.stats == nil {
		return 0, 0
	}
	return t.stats()
}

func (t *awgTunnel) HostPort() string {
	if t == nil {
		return ""
	}
	return t.server
}

var errNoAwg = errors.New("AmneziaWG на этой платформе не поддерживается")

// dialViaAwg — TCP через AmneziaWG с таймаутом.
func dialViaAwg(t *awgTunnel, host string, port int) (net.Conn, error) {
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := t.DialTCP(host, port)
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		return r.c, nil
	case <-time.After(25 * time.Second):
		return nil, errors.New("SSH через AmneziaWG не ответил за 25 секунд")
	}
}

// awgMtu — MTU туннеля: из конфига или 1420 (как в клиенте Amnezia).
func awgMtu(c *AwgConfig) int {
	if c.Iface.MTU > 0 {
		return c.Iface.MTU
	}
	return 1420
}

// awgAddrMask — адрес клиента и длина маски из конфига.
func awgAddrMask(c *AwgConfig) (netip.Addr, int, error) {
	addr := c.InterfaceAddr()
	if addr == nil {
		return netip.Addr{}, 0, errors.New("в конфиге нет IPv4-адреса клиента (Address)")
	}
	mask := 32
	for _, a := range c.Iface.Address {
		if ip, n, err := net.ParseCIDR(a); err == nil && ip.To4() != nil {
			ones, _ := n.Mask.Size()
			mask = ones
			break
		}
	}
	a, ok := netip.AddrFromSlice(addr.To4())
	if !ok {
		return netip.Addr{}, 0, errors.New("неверный IPv4-адрес клиента")
	}
	return a, mask, nil
}

// awgDns — DNS-серверы из конфига (по умолчанию 1.1.1.1 и 8.8.8.8).
func awgDns(c *AwgConfig) []netip.Addr {
	var list []netip.Addr
	for _, d := range c.Iface.DNS {
		if a, err := netip.ParseAddr(d); err == nil {
			list = append(list, a)
		}
	}
	if len(list) == 0 {
		list = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}
	}
	return list
}

// StartAWG поднимает AmneziaWG по .conf.
//
// Важно: сокеты туннеля должны идти мимо системного VPN. На Android это делается не protect(),
// а исключением приложения из своего же VPN (VpnService.Builder.addDisallowedApplication),
// поэтому ядру никаких обратных вызовов не нужно.
func StartAWG(confText string, logger Logger) (*MobileTunnel, error) {
	cfg, err := ParseAwgConf(confText)
	if err != nil {
		return nil, err
	}
	logf := func(f string, args ...any) {
		if logger != nil {
			logger.Log(sprintf(f, args...))
		}
	}
	addr, _, err := awgAddrMask(cfg)
	if err != nil {
		return nil, err
	}
	mtu := awgMtu(cfg)
	tunDev, tnet, err := netstack.CreateNetTUN([]netip.Addr{addr}, awgDns(cfg), mtu)
	if err != nil {
		return nil, err
	}
	dev := awg.NewDevice(tunDev, awgconn.NewDefaultBind(), awg.NewLogger(awg.LogLevelSilent, ""))
	t := &awgTunnel{
		stack:  &stackDialer{tnet: tnet},
		server: cfg.Peer.Endpoint,
		stats:  devStats(dev),
		stop: func() {
			// Важно: dev.Close() сам закрывает tun-устройство (amneziawg-go),
			// поэтому tunDev.Close() здесь НЕ вызывается — иначе netstack падает
			// с "panic: close of closed channel", и ядро умирает при подключении.
			_ = dev.Down()
			dev.Close()
		},
		logf: logf,
	}
	if err := dev.IpcSet(awgUAPI(cfg)); err != nil {
		t.Stop()
		return nil, err
	}
	if err := dev.Up(); err != nil {
		t.Stop()
		return nil, err
	}
	logf("AmneziaWG поднят: %s (%s), MTU %d, сервер %s", cfg.Kind(), cfg.InterfaceAddr(), mtu, cfg.Peer.Endpoint)
	return &MobileTunnel{awg: t, cfg: cfg, mtu: mtu}, nil
}

// MobileTunnel — поднятый туннель AmneziaWG с обёрткой для Java/Kotlin.
type MobileTunnel struct {
	awg *awgTunnel
	cfg *AwgConfig
	mtu int
}

// Rx — трафик туннеля: получено байт.
func (m *MobileTunnel) Rx() int64 { r, _ := m.awg.Stats(); return r }

// Tx — трафик туннеля: отправлено байт.
func (m *MobileTunnel) Tx() int64       { _, t := m.awg.Stats(); return t }
func (m *MobileTunnel) Server() string  { return m.cfg.Peer.Endpoint }
func (m *MobileTunnel) Kind() string    { return m.cfg.Kind() }
func (m *MobileTunnel) Address() string { return m.cfg.InterfaceAddr().String() }
func (m *MobileTunnel) MTU() int        { return m.mtu }

// Dial открывает TCP через туннель (адрес и порт строкой — так удобнее из Kotlin).
func (m *MobileTunnel) Dial(host string, port int) (Conn, error) {
	c, err := dialViaAwg(m.awg, host, port)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Stop опускает туннель. Повторный вызов безопасен.
func (m *MobileTunnel) Stop() {
	if m != nil && m.awg != nil {
		m.awg.Stop()
	}
}

// stackDialer — TCP через стек netstack внутри туннеля.
//
// TCP-сессии SSH целиком живут в этом стеке: наружу уходят UDP-датаграммы AmneziaWG, и они
// минуют системный VPN, потому что само приложение исключено из него (addDisallowedApplication).
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
			return nil, errors.New("у сервера " + host + " нет IPv4-адреса")
		}
	}
	return d.tnet.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(ip, uint16(port)))
}

// devStats — счётчики трафика AmneziaWG по данным устройства.
func devStats(dev *awg.Device) func() (int64, int64) {
	return func() (int64, int64) {
		s, err := dev.IpcGet()
		if err != nil {
			return 0, 0
		}
		var rx, tx int64
		for _, line := range splitLines(s) {
			switch {
			case hasPrefix(line, "rx_bytes="):
				rx += parseInt(line[len("rx_bytes="):])
			case hasPrefix(line, "tx_bytes="):
				tx += parseInt(line[len("tx_bytes="):])
			}
		}
		return rx, tx
	}
}

func parseInt(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

func itoa2(n int) string { return strconv.Itoa(n) }
