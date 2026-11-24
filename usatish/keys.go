package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Transport — способ подключения. Три свои (MTP/1-3) и два стандартных.
type Transport string

const (
	TMTP1 Transport = "MTP1"
	TMTP2 Transport = "MTP2"
	TMTP3 Transport = "MTP3"
	TWG   Transport = "WG"
	TSS   Transport = "SS"
)

var allTransports = []Transport{TMTP1, TMTP2, TMTP3, TWG, TSS}

type transportInfo struct {
	label, portKey, hint string
	own                  bool
}

var transportTable = map[Transport]transportInfo{
	TMTP1: {"MTP/1 · UDP", "udp", "основной способ, самый быстрый; UDP 443", true},
	TMTP2: {"MTP/2 · TCP", "tcp", "тот же протокол поверх TCP, для сетей, где режут UDP; TCP 8443", true},
	TMTP3: {"MTP/3 · маскировка", "mask", "тот же протокол, замаскирован под TLS; TCP 443", true},
	TWG:   {"WireGuard", "wg", "стандартный WireGuard, работает прямо в программе; UDP 51820", false},
	TSS:   {"Shadowsocks", "ss", "стандартный Shadowsocks: туннель поднимает приложение Shadowsocks, здесь копируется ссылка ss://", false},
}

func (t Transport) Valid() bool     { _, ok := transportTable[t]; return ok }
func (t Transport) Label() string   { return transportTable[t].label }
func (t Transport) PortKey() string { return transportTable[t].portKey }
func (t Transport) Hint() string    { return transportTable[t].hint }
func (t Transport) Own() bool       { return transportTable[t].own }
func (t Transport) IsMTP() bool     { return t == TMTP1 || t == TMTP2 || t == TMTP3 }

// Routing — какой трафик идёт через VPN. Для каждого ключа свой.
type Routing string

const (
	RAll   Routing = "ALL"
	RApps  Routing = "APPS"
	RNotRU Routing = "NOT_RU"
)

var routingTable = map[Routing]struct{ label, hint string }{
	RAll:   {"Весь трафик", "через VPN идут все сайты и приложения"},
	RApps:  {"Только выбранные приложения", "в Windows не поддерживается: используйте «Весь трафик» или «Кроме российских IP»"},
	RNotRU: {"Кроме российских IP", "российские адреса открываются напрямую, остальное через VPN"},
}

func (r Routing) Valid() bool   { _, ok := routingTable[r]; return ok }
func (r Routing) Label() string { return routingTable[r].label }
func (r Routing) Hint() string  { return routingTable[r].hint }

// WgParams — параметры WireGuard из ключа (ключи в base64, как в конфиге WireGuard).
type WgParams struct {
	PrivateKey      string
	ServerPublicKey string
	Address         string
	DNS             string
}

// SsParams — параметры Shadowsocks из ключа.
type SsParams struct {
	Method   string
	Password string
}

// ServerKey — разобранный ключ доступа.
type ServerKey struct {
	Name   string
	Host   string
	UID    []byte
	Secret []byte
	Ports  map[string]int
	Wg     *WgParams
	Ss     *SsParams
}

// UIDHex — идентификатор ключа на сервере (16 hex-символов).
func (k *ServerKey) UIDHex() string { return hex.EncodeToString(k.UID) }

// Port — порт выбранного способа; ok=false, если его нет в ключе.
func (k *ServerKey) Port(t Transport) (int, bool) {
	p, ok := k.Ports[t.PortKey()]
	return p, ok && p > 0
}

// Supports — есть ли в ключе всё, что нужно для способа.
func (k *ServerKey) Supports(t Transport) bool {
	if _, ok := k.Port(t); !ok {
		return false
	}
	switch t {
	case TWG:
		return k.Wg != nil
	case TSS:
		return k.Ss != nil
	}
	return true
}

const keyPrefix = "mtp://"

// keyWire — JSON внутри строки mtp:// (версия 1 — поле p число, версия 2 — объект).
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

func encodeB64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
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

// EncodeKey собирает строку mtp:// версии 2 (нужно для тестов и экспорта).
func EncodeKey(k *ServerKey) string {
	pm, _ := json.Marshal(k.Ports)
	w := keyWire{V: 2, N: k.Name, H: k.Host, U: k.UIDHex(), S: encodeB64URL(k.Secret), P: pm}
	if k.Wg != nil {
		w.W = &wgWire{K: k.Wg.PrivateKey, S: k.Wg.ServerPublicKey, A: k.Wg.Address, D: k.Wg.DNS}
	}
	if k.Ss != nil {
		w.SS = &ssWire{M: k.Ss.Method, K: k.Ss.Password}
	}
	b, _ := json.Marshal(w)
	return keyPrefix + encodeB64URL(b)
}

// SSLink — ссылка ss:// (SIP002) для официального приложения Shadowsocks.
func SSLink(k *ServerKey) (string, bool) {
	if k.Ss == nil {
		return "", false
	}
	port, ok := k.Port(TSS)
	if !ok {
		return "", false
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(k.Ss.Method + ":" + k.Ss.Password))
	name := k.Name
	if name == "" {
		name = "usatyi"
	}
	tag := strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	return fmt.Sprintf("ss://%s@%s:%d#%s", userinfo, k.Host, port, tag), true
}

// DefaultTransport — первый способ из ключа: сначала свой MTP/1.
func DefaultTransport(k *ServerKey) Transport {
	for _, t := range allTransports {
		if k.Supports(t) {
			return t
		}
	}
	return TMTP1
}

// SavedKey — ключ, сохранённый на компьютере, со своими настройками.
// KeyStat — последние данные сервера по ключу. Обновляется при каждом STATUS, пока ключ подключён,
// чтобы трафик и срок были видны и без подключения.
type KeyStat struct {
	Used    int64 `json:"used"`
	Limit   int64 `json:"limit"`
	Expires int64 `json:"expires"`
	At      int64 `json:"at"`
}

type SavedKey struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Alias     string    `json:"alias"`
	Transport Transport `json:"transport"`
	Routing   Routing   `json:"routing"`
	Apps      []string  `json:"apps"`
	Stat      *KeyStat  `json:"stat,omitempty"`
}

// DisplayName — название для показа: своё, иначе имя из ключа.
func (s SavedKey) DisplayName() string {
	if s.Alias != "" {
		return s.Alias
	}
	if k, err := ParseKey(s.Text); err == nil && k.Name != "" {
		return k.Name
	}
	return "Без имени"
}

type keyFile struct {
	Keys     []SavedKey `json:"keys"`
	Selected string     `json:"selected"`
}

// KeyStore — список ключей на компьютере (JSON-файл).
type KeyStore struct {
	mu   sync.Mutex
	path string
	data keyFile
}

// OpenKeyStore открывает файл со списком ключей (если его нет — пустой список).
func OpenKeyStore(path string) (*KeyStore, error) {
	ks := &KeyStore{path: path}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &ks.data); err != nil {
			return nil, fmt.Errorf("файл ключей повреждён: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return ks, nil
}

func (ks *KeyStore) save() error {
	b, err := json.MarshalIndent(ks.data, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ks.path), 0o700); err != nil {
		return err
	}
	tmp := ks.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ks.path)
}

// List — все ключи в порядке добавления.
func (ks *KeyStore) List() []SavedKey {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	out := make([]SavedKey, len(ks.data.Keys))
	copy(out, ks.data.Keys)
	return out
}

// Find — ключ по идентификатору.
func (ks *KeyStore) Find(id string) (SavedKey, bool) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for _, k := range ks.data.Keys {
		if k.ID == id {
			return k, true
		}
	}
	return SavedKey{}, false
}

// SetStat запоминает данные сервера для ключа. Файл пишется, только если пришёл новый STATUS (st.At).
func (ks *KeyStore) SetStat(id string, st serverStatus) error {
	if st.At == 0 {
		return nil
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.data.Keys {
		k := &ks.data.Keys[i]
		if k.ID != id {
			continue
		}
		if k.Stat != nil && k.Stat.At == st.At {
			return nil
		}
		k.Stat = &KeyStat{Used: st.Used, Limit: st.Limit, Expires: st.Expires, At: st.At}
		return ks.save()
	}
	return nil
}

// Selected — идентификатор выбранного ключа ("" если не выбран).
func (ks *KeyStore) Selected() string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.data.Selected
}

// Select — выбирает ключ (пустой id снимает выбор).
func (ks *KeyStore) Select(id string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if id != "" {
		found := false
		for _, k := range ks.data.Keys {
			if k.ID == id {
				found = true
				break
			}
		}
		if !found {
			return errors.New("ключ не найден")
		}
	}
	ks.data.Selected = id
	return ks.save()
}

// AddOrUpdate добавляет ключ. Если такой ключ уже есть, заменяет строку и сохраняет настройки.
// Возвращает ключ, признак «новый» и признак «строка обновлена».
func (ks *KeyStore) AddOrUpdate(text string) (SavedKey, bool, bool, error) {
	clean := strings.TrimSpace(text)
	k, err := ParseKey(clean)
	if err != nil {
		return SavedKey{}, false, false, err
	}
	id := k.UIDHex()
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.data.Keys {
		if ks.data.Keys[i].ID == id {
			if ks.data.Keys[i].Text == clean {
				return ks.data.Keys[i], false, false, nil
			}
			ks.data.Keys[i].Text = clean
			return ks.data.Keys[i], false, true, ks.save()
		}
	}
	sk := SavedKey{ID: id, Text: clean, Transport: DefaultTransport(k), Routing: RAll, Apps: []string{}}
	ks.data.Keys = append(ks.data.Keys, sk)
	if ks.data.Selected == "" {
		ks.data.Selected = id
	}
	return sk, true, false, ks.save()
}

// Update сохраняет название, способ, маршрутизацию и приложения ключа.
func (ks *KeyStore) Update(sk SavedKey) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.data.Keys {
		if ks.data.Keys[i].ID == sk.ID {
			ks.data.Keys[i].Alias = sk.Alias
			ks.data.Keys[i].Transport = sk.Transport
			ks.data.Keys[i].Routing = sk.Routing
			ks.data.Keys[i].Apps = sk.Apps
			return ks.save()
		}
	}
	return errors.New("ключ не найден")
}

// Remove удаляет ключ с компьютера (на сервере он остаётся).
func (ks *KeyStore) Remove(id string) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	kept := ks.data.Keys[:0]
	for _, k := range ks.data.Keys {
		if k.ID != id {
			kept = append(kept, k)
		}
	}
	ks.data.Keys = kept
	if ks.data.Selected == id {
		ks.data.Selected = ""
	}
	return ks.save()
}
