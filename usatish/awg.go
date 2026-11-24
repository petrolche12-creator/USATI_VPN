package main

// API AmneziaWG-туннеля, общий для всех платформ. Реализация:
//   - awg_windows.go — WinTun-адаптер и стек netstack внутри процесса;
//   - awg_other.go   — только для тестов на Linux (TUN в сетевом пространстве).

import (
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const awgAdapterName = "UsatyiAWG"

// awgTunnel — поднятый AmneziaWG: сокеты наружу идут через его собственный стек (netstack).
// Поэтому SSH-соединение живёт внутри туннеля и не зависит от маршрутов Windows.
type awgTunnel struct {
	stack  netDialer
	server string // host:port сервера — для показа и диагностики
	stats  func() (rx, tx int64)
	stop   func()
	once   sync.Once
	logf   func(string, ...any)
}

// netDialer — минимальный интерфейс dialer'а netstack (для тестов подменяется).
type netDialer interface {
	DialTCP(host string, port int) (net.Conn, error)
}

// Stop опускает туннель. Вызывать можно несколько раз.
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

// DialTCP — TCP-соединение через туннель.
func (t *awgTunnel) DialTCP(host string, port int) (net.Conn, error) {
	return t.stack.DialTCP(host, port)
}

// Stats — трафик AmneziaWG по данным устройства.
func (t *awgTunnel) Stats() (rx, tx int64) {
	if t == nil || t.stats == nil {
		return 0, 0
	}
	return t.stats()
}

// HostPort — адрес сервера AmneziaWG.
func (t *awgTunnel) HostPort() string {
	if t == nil {
		return ""
	}
	return t.server
}

// awgStarter создаёт туннель. routeAll — направить в туннель весь трафик компьютера.
// Задаётся платформой; в тестах подменяется.
var awgStarter func(c *AwgConfig, routeAll bool, logf func(string, ...any)) (*awgTunnel, error)

var errNoAwg = errors.New("AmneziaWG на этой платформе не поддерживается")

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

// awgUAPI — конфигурация устройства в формате UAPI amneziawg-go (ключи в hex).
// Поля версии 3 (header_protection_key и дальше) передаются как есть: если сервер их не
// использует, они пустые и в строку не попадают.
func awgUAPI(c *AwgConfig) string {
	var b strings.Builder
	priv, _ := awgKey32(c.Iface.PrivateKey)
	pub, _ := awgKey32(c.Peer.PublicKey)
	b.WriteString("private_key=" + hexOf(priv) + "\n")
	setUAPI(&b, "jc", c.Iface.Jc)
	setUAPI(&b, "jmin", c.Iface.Jmin)
	setUAPI(&b, "jmax", c.Iface.Jmax)
	setUAPI(&b, "s1", c.Iface.S1)
	setUAPI(&b, "s2", c.Iface.S2)
	setUAPI(&b, "s3", c.Iface.S3)
	setUAPI(&b, "s4", c.Iface.S4)
	setUAPI(&b, "h1", c.Iface.H1)
	setUAPI(&b, "h2", c.Iface.H2)
	setUAPI(&b, "h3", c.Iface.H3)
	setUAPI(&b, "h4", c.Iface.H4)
	setUAPI(&b, "i1", c.Iface.I1)
	setUAPI(&b, "i2", c.Iface.I2)
	setUAPI(&b, "i3", c.Iface.I3)
	setUAPI(&b, "i4", c.Iface.I4)
	setUAPI(&b, "i5", c.Iface.I5)
	setUAPI(&b, "header_protection_key", c.Iface.HeaderProtectionKey)
	setUAPI(&b, "content_padding_addition", c.Iface.ContentPaddingAddition)
	setUAPI(&b, "rekey_after_time", c.Iface.RekeyAfterTime)
	setUAPI(&b, "rekey_timeout", c.Iface.RekeyTimeout)
	setUAPI(&b, "reject_after_time", c.Iface.RejectAfterTime)
	setUAPI(&b, "keepalive_timeout", c.Iface.KeepaliveTimeout)
	setUAPI(&b, "max_handshake_attempts", c.Iface.MaxHandshakeAttempts)
	setUAPI(&b, "random_trailers", c.Iface.RandomTrailers)
	setUAPI(&b, "disable_cookies", c.Iface.DisableCookies)
	b.WriteString("replace_peers=true\n")
	b.WriteString("public_key=" + hexOf(pub) + "\n")
	if strings.TrimSpace(c.Peer.PresharedKey) != "" {
		if psk, err := awgKey32(c.Peer.PresharedKey); err == nil {
			b.WriteString("preshared_key=" + hexOf(psk) + "\n")
		}
	}
	b.WriteString("endpoint=" + c.Peer.Endpoint + "\n")
	ka := c.Peer.Keepalive
	if ka <= 0 {
		ka = 25
	}
	b.WriteString("persistent_keepalive_interval=" + itoa(ka) + "\n")
	b.WriteString("replace_allowed_ips=true\n")
	for _, a := range c.Peer.AllowedIPs {
		if strings.Contains(a, ":") {
			continue // IPv6 не нужен: MTP-VPN работает только по IPv4
		}
		b.WriteString("allowed_ip=" + a + "\n")
	}
	return b.String()
}

func setUAPI(b *strings.Builder, key, val string) {
	if v := strings.TrimSpace(val); v != "" {
		b.WriteString(key + "=" + v + "\n")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

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

// awgKey32 — ключ base64 (32 байта) в байты.
func awgKey32(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("ключ должен быть 32 байта")
	}
	return b, nil
}
