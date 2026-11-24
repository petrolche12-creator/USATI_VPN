package core

// Конфигурация AmneziaWG (.conf из приложения Amnezia, раздел «Поделиться»).
// Разбирается без внешних библиотек и проверяется так же, как в клиенте Amnezia:
// обязательные поля Interface (PrivateKey, Address) и Peer (PublicKey, Endpoint, AllowedIPs),
// параметры обфускации Jc/Jmin/Jmax, S1–S4, H1–H4, I1–I5 и поля версии 3 (передаются в UAPI как есть).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// AwgIface — интерфейс из .conf (сервер-транспорт).
type AwgIface struct {
	Name       string // исходное имя секции, обычно amneziawg
	PrivateKey string
	Address    []string // 10.8.0.2/32, может быть несколько
	DNS        []string
	MTU        int
	ListenPort int

	// Обфускация AmneziaWG (диапазоны H и интервалы J передаются строкой как в конфиге).
	Jc, Jmin, Jmax                                                                        string
	S1, S2, S3, S4                                                                        string
	H1, H2, H3, H4                                                                        string
	I1, I2, I3, I4, I5                                                                    string
	HeaderProtectionKey                                                                   string
	ContentPaddingAddition                                                                string
	RekeyAfterTime, RekeyTimeout, RejectAfterTime, KeepaliveTimeout, MaxHandshakeAttempts string
	RandomTrailers, DisableCookies                                                        string
}

// AwgPeer — сервер (Peer) из .conf.
type AwgPeer struct {
	PublicKey    string
	PresharedKey string
	Endpoint     string // host:port
	AllowedIPs   []string
	Keepalive    int
}

// AwgConfig — полный .conf.
type AwgConfig struct {
	Iface AwgIface
	Peer  AwgPeer
	Path  string // откуда прочитан (для показа)
}

// Name — имя для списка: имя файла без расширения.
func (c *AwgConfig) DisplayName() string {
	base := c.Path
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".conf"), ".txt")
	if base == "" {
		base = "Amnezia"
	}
	return base
}

// Host — адрес сервера без порта.
func (c *AwgConfig) Host() string {
	h, _, err := net.SplitHostPort(c.Peer.Endpoint)
	if err != nil {
		return c.Peer.Endpoint
	}
	return h
}

// Port — UDP-порт сервера из Endpoint.
func (c *AwgConfig) Port() int {
	_, p, err := net.SplitHostPort(c.Peer.Endpoint)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

// InterfaceAddr — первый IPv4 адрес клиента в туннеле (без длины маски).
func (c *AwgConfig) InterfaceAddr() net.IP {
	for _, a := range c.Iface.Address {
		ipStr := a
		if i := strings.Index(a, "/"); i >= 0 {
			ipStr = a[:i]
		}
		if ip := net.ParseIP(strings.TrimSpace(ipStr)); ip != nil && ip.To4() != nil {
			return ip.To4()
		}
	}
	return nil
}

// hasObf — заданы ли параметры обфускации (значит, это AmneziaWG, а не обычный WireGuard).
func (c *AwgConfig) hasObf() bool {
	return c.Iface.Jc != "" || c.Iface.Jmin != "" || c.Iface.H1 != "" || c.Iface.S1 != ""
}

// Kind — «AmneziaWG» или «WireGuard» — для показа пользователю.
func (c *AwgConfig) Kind() string {
	if c.hasObf() {
		return "AmneziaWG"
	}
	return "WireGuard"
}

// Validate проверяет обязательные поля и форматы ключей.
func (c *AwgConfig) Validate() error {
	if c.Iface.PrivateKey == "" {
		return errors.New("в конфиге нет PrivateKey в секции [Interface]")
	}
	if err := checkKey32(c.Iface.PrivateKey); err != nil {
		return fmt.Errorf("неверный PrivateKey: %w", err)
	}
	if len(c.Iface.Address) == 0 {
		return errors.New("в конфиге нет Address в секции [Interface]")
	}
	if c.InterfaceAddr() == nil {
		return errors.New("в конфиге нет IPv4-адреса в Address: нужен адрес вида 10.8.0.2/32")
	}
	if c.Peer.PublicKey == "" {
		return errors.New("в конфиге нет PublicKey в секции [Peer]")
	}
	if err := checkKey32(c.Peer.PublicKey); err != nil {
		return fmt.Errorf("неверный PublicKey сервера: %w", err)
	}
	if c.Peer.PresharedKey != "" && strings.TrimSpace(c.Peer.PresharedKey) != "" {
		if err := checkKey32(c.Peer.PresharedKey); err != nil {
			return fmt.Errorf("неверный PresharedKey: %w", err)
		}
	}
	if c.Peer.Endpoint == "" {
		return errors.New("в конфиге нет Endpoint в секции [Peer]")
	}
	h, p, err := net.SplitHostPort(c.Peer.Endpoint)
	if err != nil || h == "" || p == "" {
		return fmt.Errorf("неверный Endpoint %q: нужен адрес и порт, например 1.2.3.4:51820", c.Peer.Endpoint)
	}
	if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("неверный порт в Endpoint: %q", p)
	}
	if len(c.Peer.AllowedIPs) == 0 {
		return errors.New("в конфиге нет AllowedIPs в секции [Peer]")
	}
	return nil
}

func checkKey32(s string) error {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return errors.New("ключ должен быть в base64")
	}
	if len(b) != 32 {
		return fmt.Errorf("ключ должен быть 32 байта, а не %d", len(b))
	}
	return nil
}

// ParseAwgConf разбирает текст .conf. Понимает и amneziawg:// (base64 от текста), и путь/URL не нужны.
func ParseAwgConf(text string) (*AwgConfig, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if strings.HasPrefix(text, "amneziawg://") || strings.HasPrefix(text, "wireguard://") {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimPrefix(text, "amneziawg://"), "wireguard://"))
		if err != nil {
			return nil, errors.New("ссылка amneziawg:// повреждена")
		}
		text = string(raw)
	}
	c := &AwgConfig{}
	section := ""
	for _, lineRaw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(lineRaw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.Trim(line, "[]"))
			if section == "interface" || strings.HasPrefix(section, "amneziawg") || strings.HasPrefix(section, "wireguard") {
				if c.Iface.Name == "" {
					c.Iface.Name = strings.Trim(line, "[]")
				}
				section = "interface"
			} else if section == "peer" {
				section = "peer"
			} else {
				section = ""
			}
			continue
		}
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		// значения могу содержать «;» как комментарий — но только не base64 ключи
		if j := strings.Index(val, " #"); j >= 0 {
			val = strings.TrimSpace(val[:j])
		}
		if val == "" {
			continue
		}
		lkey := strings.ToLower(key)
		switch section {
		case "interface":
			switch lkey {
			case "privatekey":
				c.Iface.PrivateKey = val
			case "address":
				c.Iface.Address = splitList(val)
			case "dns":
				c.Iface.DNS = splitList(val)
			case "mtu":
				c.Iface.MTU, _ = strconv.Atoi(val)
			case "listenport":
				c.Iface.ListenPort, _ = strconv.Atoi(val)
			case "jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4",
				"i1", "i2", "i3", "i4", "i5", "headerprotectionkey", "contentpaddingaddition",
				"rekeyaftertime", "rekeytimeout", "rejectaftertime", "keepalivetimeout",
				"maxhandshakeattempts", "randomtrailers", "disablecookies":
				setAwgField(&c.Iface, lkey, val)
			}
		case "peer":
			switch lkey {
			case "publickey":
				c.Peer.PublicKey = val
			case "presharedkey":
				c.Peer.PresharedKey = val
			case "endpoint":
				c.Peer.Endpoint = val
			case "allowedips":
				c.Peer.AllowedIPs = splitList(val)
			case "persistentkeepalive":
				c.Peer.Keepalive, _ = strconv.Atoi(val)
			}
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func setAwgField(f *AwgIface, key, val string) {
	switch key {
	case "jc":
		f.Jc = val
	case "jmin":
		f.Jmin = val
	case "jmax":
		f.Jmax = val
	case "s1":
		f.S1 = val
	case "s2":
		f.S2 = val
	case "s3":
		f.S3 = val
	case "s4":
		f.S4 = val
	case "h1":
		f.H1 = val
	case "h2":
		f.H2 = val
	case "h3":
		f.H3 = val
	case "h4":
		f.H4 = val
	case "i1":
		f.I1 = val
	case "i2":
		f.I2 = val
	case "i3":
		f.I3 = val
	case "i4":
		f.I4 = val
	case "i5":
		f.I5 = val
	case "headerprotectionkey":
		f.HeaderProtectionKey = val
	case "contentpaddingaddition":
		f.ContentPaddingAddition = val
	case "rekeyaftertime":
		f.RekeyAfterTime = val
	case "rekeytimeout":
		f.RekeyTimeout = val
	case "rejectaftertime":
		f.RejectAfterTime = val
	case "keepalivetimeout":
		f.KeepaliveTimeout = val
	case "maxhandshakeattempts":
		f.MaxHandshakeAttempts = val
	case "randomtrailers":
		f.RandomTrailers = val
	case "disablecookies":
		f.DisableCookies = val
	}
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseAwgLink понимает ссылку vpn://... из Amnezia, если внутри есть текст конфига.
// На практике приложение отдаёт файл .conf; ссылка поддерживается как запасной путь.
func ParseAwgLink(link string) (*AwgConfig, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "vpn" {
		return nil, errors.New("это не ссылка vpn://")
	}
	return nil, errors.New("ссылки vpn:// пока не поддерживаются: сохраните конфиг файлом .conf из Amnezia")
}
