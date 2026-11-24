package core

// Ключи доступа и форматы данных, которые нужны Java/Kotlin. Здесь только разбор и хранение
// в строках: файлы, настройки и интерфейс остаются на стороне Android.

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Transport — способ подключения: три своих (MTP/1-3) и два стандартных.
type Transport string

const (
	TMTP1 Transport = "MTP1"
	TMTP2 Transport = "MTP2"
	TMTP3 Transport = "MTP3"
	TWG   Transport = "WG"
	TSS   Transport = "SS"
)

var transportTable = map[Transport]struct{ label, portKey string }{
	TMTP1: {"MTP/1 · UDP", "udp"},
	TMTP2: {"MTP/2 · TCP", "tcp"},
	TMTP3: {"MTP/3 · маскировка", "mask"},
	TWG:   {"WireGuard", "wg"},
	TSS:   {"Shadowsocks", "ss"},
}

func (t Transport) Label() string   { return transportTable[t].label }
func (t Transport) PortKey() string { return transportTable[t].portKey }
func (t Transport) IsMTP() bool     { return t == TMTP1 || t == TMTP2 || t == TMTP3 }

// Routing — какой трафик идёт через VPN.
type Routing string

const (
	RAll   Routing = "ALL"
	RApps  Routing = "APPS"
	RNotRU Routing = "NOT_RU"
)

// Owner — доступ по SSH к своему серверу.
type Owner struct {
	Host    string
	SSHPort int
	SSHUser string
	SSHPass string
	VPNPort int
	IsRoot  bool
	HostFP  string
}

func (o *Owner) target() sshTarget {
	return sshTarget{Host: o.Host, Port: o.SSHPort, User: o.SSHUser, Pass: o.SSHPass, PinnedFP: o.HostFP}
}

// ServerKey — разобранный ключ mtp://.
type ServerKey struct {
	Name   string
	Host   string
	UID    []byte
	Secret []byte
	Ports  map[string]int
	Wg     *WgParams
	Ss     *SsParams
}

type WgParams struct {
	PrivateKey      string
	ServerPublicKey string
	Address         string
	DNS             string
}

type SsParams struct {
	Method   string
	Password string
}

// Port — порт выбранного способа; ok=false, если его нет в ключе.
func (k *ServerKey) Port(t Transport) (int, bool) {
	p, ok := k.Ports[t.PortKey()]
	return p, ok && p > 0
}

// Supports — есть ли в ключе всё, что нужно для способа.
func (k *ServerKey) Supports(t Transport) bool {
	switch t {
	case TWG:
		_, ok := k.Port(TWG)
		return ok && k.Wg != nil
	case TSS:
		_, ok := k.Port(TSS)
		return ok && k.Ss != nil
	default:
		_, ok := k.Port(t)
		return ok
	}
}

const keyPrefix = "mtp://"

type keyWire struct {
	V  int             `json:"v"`
	N  string          `json:"n"`
	H  string          `json:"h"`
	U  string          `json:"u"`
	S  string          `json:"s"`
	P  json.RawMessage `json:"p"`
	W  *wgWire         `json:"w,omitempty"`
	SS *ssWire         `json:"ss,omitempty"`
}

type wgWire struct {
	K string `json:"k"`
	S string `json:"s"`
	A string `json:"a"`
	D string `json:"d"`
}

type ssWire struct {
	M string `json:"m"`
	K string `json:"k"`
}

func decodeB64URL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}

// ParseKey разбирает строку mtp:// версий 1 и 2.
func ParseKey(text string) (*ServerKey, error) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, keyPrefix) {
		return nil, errors.New("ключ должен начинаться с mtp://")
	}
	raw, err := decodeB64URL(t[len(keyPrefix):])
	if err != nil {
		return nil, errors.New("ключ повреждён")
	}
	var w keyWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, errors.New("ключ повреждён")
	}
	uid, err := hex.DecodeString(w.U)
	if err != nil || len(uid) != 8 {
		return nil, errors.New("ключ повреждён: неверный идентификатор")
	}
	secret, err := decodeB64URL(w.S)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("ключ повреждён: неверный секрет")
	}
	if strings.TrimSpace(w.H) == "" {
		return nil, errors.New("в ключе нет адреса сервера")
	}
	ports := map[string]int{}
	p := bytes.TrimSpace(w.P)
	if len(p) > 0 && p[0] == '{' {
		if err := json.Unmarshal(p, &ports); err != nil {
			return nil, errors.New("ключ повреждён: порты")
		}
	} else {
		var port int
		if err := json.Unmarshal(p, &port); err != nil || port <= 0 {
			return nil, errors.New("ключ повреждён: порт")
		}
		ports["udp"] = port
	}
	k := &ServerKey{Name: w.N, Host: strings.TrimSpace(w.H), UID: uid, Secret: secret, Ports: ports}
	if w.W != nil {
		k.Wg = &WgParams{PrivateKey: w.W.K, ServerPublicKey: w.W.S, Address: w.W.A, DNS: w.W.D}
		if k.Wg.DNS == "" {
			k.Wg.DNS = "1.1.1.1"
		}
	}
	if w.SS != nil {
		k.Ss = &SsParams{Method: w.SS.M, Password: w.SS.K}
	}
	return k, nil
}

// KeyInfo — сведения о ключе для экрана (без секрета).
type KeyInfo struct {
	Name     string
	Host     string
	Supports string
}

// InspectKey разбирает ключ и говорит, какие способы в нём есть.
func InspectKey(text string) (*KeyInfo, error) {
	k, err := ParseKey(text)
	if err != nil {
		return nil, err
	}
	var list []string
	for _, t := range []Transport{TMTP1, TMTP2, TMTP3, TWG, TSS} {
		if k.Supports(t) {
			list = append(list, t.Label())
		}
	}
	return &KeyInfo{Name: k.Name, Host: k.Host, Supports: strings.Join(list, ", ")}, nil
}

// SSLink — ссылка ss:// (SIP002) для приложения Shadowsocks.
func SSLink(text string) (string, error) {
	k, err := ParseKey(text)
	if err != nil {
		return "", err
	}
	if k.Ss == nil {
		return "", errors.New("в ключе нет параметров Shadowsocks")
	}
	port, ok := k.Port(TSS)
	if !ok {
		return "", errors.New("в ключе нет порта Shadowsocks")
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(k.Ss.Method + ":" + k.Ss.Password))
	name := k.Name
	if name == "" {
		name = "usatyi"
	}
	return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, k.Host, port, urlQueryEscape(name)), nil
}

// urlQueryEscape — экранирование имени в якоре ссылки (#...).
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteRune(r)
			continue
		}
		for _, c := range []byte(string(r)) {
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	out := b.String()
	return strings.ReplaceAll(out, "+", "%20")
}
