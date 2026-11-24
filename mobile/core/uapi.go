package core

// Конфигурация устройства AmneziaWG в формате UAPI: ключи в hex, параметры обфускации — как в .conf.

import (
	"encoding/base64"
	"errors"
	"strings"
)

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
