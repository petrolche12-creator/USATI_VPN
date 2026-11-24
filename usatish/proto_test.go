package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// Векторы посчитаны на сервере (Python cryptography) и совпадают с Android-тестами.

func TestHKDFMatchesServer(t *testing.T) {
	okm := make([]byte, 64)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte("ikm-material"), []byte("salt-salt"), []byte("mtp1 keys")), okm); err != nil {
		t.Fatal(err)
	}
	want := "d8e9171bf3129e1a33ec9adf91fe90e46c9fc2654ae0a2ba860837ba1cc4606bacaa6c2bde2b51fa64abc8b86564a5b81fcc47ceb2a5af6a226bc154950cc98c"
	if got := hex.EncodeToString(okm); got != want {
		t.Fatalf("HKDF: %s", got)
	}
}

func TestChaCha20Poly1305MatchesServer(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	aad := make([]byte, 13)
	for i := range aad {
		aad[i] = byte(i)
	}
	c, err := chacha20poly1305.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct := c.Seal(nil, nonce12(5), []byte("hello world, mtp/1"), aad)
	if got, want := hex.EncodeToString(ct), "a4ad8f704dbfee5cdbe2279cec717d0cbf2f22ab3f774c76d069157514d6d4ce4608"; got != want {
		t.Fatalf("ChaCha20-Poly1305: %s", got)
	}
	if _, err := c.Open(nil, nonce12(6), ct, aad); err == nil {
		t.Fatal("тег должен не сойтись с другим счётчиком")
	}
}

func TestTag16MatchesServer(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	got := hex.EncodeToString(tag16(key, []byte("mtp1-hello"), []byte("abc")))
	if want := "f58c68bec3be5d3f184a44b2249801fc"; got != want {
		t.Fatalf("tag16: %s", got)
	}
}

func TestReplayWindow(t *testing.T) {
	var w replayWindow
	if !w.ok(0) {
		t.Fatal("первый пакет должен пройти")
	}
	w.mark(0)
	if w.ok(0) {
		t.Fatal("повтор должен отбрасываться")
	}
	if !w.ok(5) {
		t.Fatal("новый номер должен пройти")
	}
	w.mark(5)
	if !w.ok(3) {
		t.Fatal("номер внутри окна, ещё не принятый, должен пройти")
	}
	w.mark(3)
	if w.ok(3) {
		t.Fatal("повтор внутри окна должен отбрасываться")
	}
	w.mark(100)
	if w.ok(36) {
		t.Fatal("номер старше окна в 64 пакета должен отбрасываться")
	}
	if !w.ok(37) {
		t.Fatal("номер на границе окна должен пройти")
	}
}

func TestTCPFraming(t *testing.T) {
	frame := make([]byte, 300)
	for i := range frame {
		frame[i] = byte(i)
	}
	wire := encodeTCP(frame)
	if _, n, err := parseFrame(wire[:len(wire)-1], false); err != nil || n != 0 {
		t.Fatalf("неполный кадр: n=%d err=%v", n, err)
	}
	got, n, err := parseFrame(wire, false)
	if err != nil || n != len(wire) || !bytes.Equal(got, frame) {
		t.Fatalf("кадр MTP/2 не разобран: n=%d err=%v", n, err)
	}
}

func TestMaskFramingLooksLikeTLS(t *testing.T) {
	data := append([]byte{tData}, bytes.Repeat([]byte{5}, 100)...)
	hello := append([]byte{tHello}, bytes.Repeat([]byte{6}, 97)...)
	wire, err := encodeMask(data)
	if err != nil {
		t.Fatal(err)
	}
	if wire[0] != 0x17 || wire[1] != 0x03 || wire[2] != 0x03 {
		t.Fatalf("заголовок записи: % x", wire[:3])
	}
	got, n, err := parseFrame(wire, true)
	if err != nil || n != len(wire) || !bytes.Equal(got, data) {
		t.Fatalf("запись данных: n=%d err=%v", n, err)
	}
	wireHello, err := encodeMask(hello)
	if err != nil {
		t.Fatal(err)
	}
	if wireHello[0] != 0x16 {
		t.Fatalf("рукопожатие должно идти в записи 0x16, получено %x", wireHello[0])
	}
	got, _, err = parseFrame(wireHello, true)
	if err != nil || !bytes.Equal(got, hello) {
		t.Fatalf("рукопожатие не разобрано: %v", err)
	}
	if _, n, _ := parseFrame(wire[:4], true); n != 0 {
		t.Fatal("неполная запись не должна разбираться")
	}
}

// Сессия, поднятая без сети: шифрование и расшифровка пары сторон.
func TestSessionEncryptDecryptRoundTrip(t *testing.T) {
	okm := make([]byte, 64)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte("a"), []byte("b"), []byte("mtp1 keys")), okm); err != nil {
		t.Fatal(err)
	}
	c2s, _ := chacha20poly1305.New(okm[:32])
	s2c, _ := chacha20poly1305.New(okm[32:])
	client := &Session{tx: c2s, rx: s2c}
	server := &Session{tx: s2c, rx: c2s}
	copy(client.SID[:], []byte{1, 2, 3, 4})
	copy(server.SID[:], []byte{1, 2, 3, 4})
	for i := 0; i < 3; i++ {
		frame := client.Encrypt(kIP, []byte("пакет"))
		kind, payload, ok := server.Decrypt(frame)
		if !ok || kind != kIP || string(payload) != "пакет" {
			t.Fatalf("пакет %d не расшифрован", i)
		}
	}
	// повтор того же кадра отбрасывается
	frame := client.Encrypt(kKeepalive, nil)
	if _, _, ok := server.Decrypt(frame); !ok {
		t.Fatal("первый кадр должен пройти")
	}
	if _, _, ok := server.Decrypt(frame); ok {
		t.Fatal("повтор должен отбрасываться")
	}
}

func TestStatusParse(t *testing.T) {
	st, ok := parseStatus([]byte(`{"name":"Вася","used":1234,"limit":5000,"expires":0,"ip":"10.8.0.2"}`))
	if !ok || st.Used != 1234 || st.Limit != 5000 || st.Expires != 0 {
		t.Fatalf("STATUS: %+v ok=%v", st, ok)
	}
	if _, err := json.Marshal(st); err != nil {
		t.Fatal(err)
	}
}

// --- ключи

func TestKeyRoundTripV2(t *testing.T) {
	k := &ServerKey{
		Name: "Вася", Host: "203.0.113.5", UID: bytes.Repeat([]byte{1}, 8), Secret: bytes.Repeat([]byte{2}, 32),
		Ports: map[string]int{"udp": 443, "tcp": 8443, "mask": 443, "wg": 51820, "ss": 20001},
		Wg:    &WgParams{PrivateKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), ServerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), Address: "10.9.0.2/32", DNS: "1.1.1.1"},
		Ss:    &SsParams{Method: "chacha20-ietf-poly1305", Password: "пароль-123"},
	}
	back, err := ParseKey(EncodeKey(k))
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != "Вася" || back.Host != "203.0.113.5" || back.UIDHex() != k.UIDHex() {
		t.Fatalf("поля ключа: %+v", back)
	}
	if p, _ := back.Port(TMTP2); p != 8443 {
		t.Fatalf("порт MTP/2: %d", p)
	}
	if !back.Supports(TWG) || !back.Supports(TSS) || back.Wg.Address != "10.9.0.2/32" {
		t.Fatal("способы из ключа не распознаны")
	}
	if DefaultTransport(back) != TMTP1 {
		t.Fatal("по умолчанию должен быть MTP/1")
	}
}

func TestKeyV1StillParses(t *testing.T) {
	j, _ := json.Marshal(map[string]any{"v": 1, "h": "198.51.100.7", "p": 4433,
		"u": hex.EncodeToString(bytes.Repeat([]byte{1}, 8)),
		"s": base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), "n": "Петя"})
	k, err := ParseKey("mtp://" + base64.RawURLEncoding.EncodeToString(j))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := k.Port(TMTP1); !ok || p != 4433 {
		t.Fatalf("порт версии 1: %d %v", p, ok)
	}
	if k.Supports(TWG) || k.Supports(TMTP2) {
		t.Fatal("в ключе версии 1 есть только MTP/1")
	}
}

func TestKeyRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "это не ключ", "mtp://!!!", "mtp://" + base64.RawURLEncoding.EncodeToString([]byte(`{"u":"zz"}`))} {
		if _, err := ParseKey(bad); err == nil {
			t.Fatalf("ключ %q должен не разбираться", bad)
		}
	}
}

func TestSSLinkFormat(t *testing.T) {
	k := &ServerKey{Name: "Вася", Host: "203.0.113.5", Ports: map[string]int{"ss": 20001}, Ss: &SsParams{Method: "chacha20-ietf-poly1305", Password: "pw"}}
	link, ok := SSLink(k)
	if !ok || !strings.HasPrefix(link, "ss://") {
		t.Fatalf("ссылка: %q", link)
	}
	userinfo := strings.TrimPrefix(strings.SplitN(link, "@", 2)[0], "ss://")
	raw, err := base64.RawURLEncoding.DecodeString(userinfo)
	if err != nil || string(raw) != "chacha20-ietf-poly1305:pw" {
		t.Fatalf("userinfo: %q %v", raw, err)
	}
	if !strings.Contains(link, "@203.0.113.5:20001#") {
		t.Fatalf("адрес в ссылке: %q", link)
	}
}

func TestRoutesForModes(t *testing.T) {
	allowed, tun, err := routesFor(RAll, nil)
	if err != nil || len(allowed) == 0 || !contains(tun, "0.0.0.0/1") {
		t.Fatalf("весь трафик: %v %v", tun, err)
	}
	if _, _, err := routesFor(RApps, nil); err == nil {
		t.Fatal("режим приложений в Windows должен отказывать")
	}
	if len(notRUList) < 1000 {
		t.Fatalf("список российских сетей короткий: %d", len(notRUList))
	}
	allowed, tun, err = routesFor(RNotRU, notRUList)
	if err != nil || len(tun) != len(notRUList) || len(allowed) != len(notRUList) {
		t.Fatalf("кроме российских IP: %d/%d %v", len(tun), len(allowed), err)
	}
	for _, c := range allowed {
		if strings.Contains(c, ":") {
			t.Fatalf("IPv6 не должен попадать в маршруты: %s", c)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// --- AmneziaWG: конфигурация устройства

func TestAwgUAPI(t *testing.T) {
	conf := `[Interface]
PrivateKey = ` + testKey(1) + `
Address = 10.66.66.2/32
Jc = 5
Jmin = 30
Jmax = 512
S1 = 40
S2 = 50
H1 = 123456
H2 = 234567

[Peer]
PublicKey = ` + testKey(2) + `
Endpoint = 203.0.113.5:51820
AllowedIPs = 0.0.0.0/0
`
	cfg, err := ParseAwgConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	s := awgUAPI(cfg)
	for _, want := range []string{
		"private_key=0101", "public_key=0202", "endpoint=203.0.113.5:51820",
		"persistent_keepalive_interval=25", "allowed_ip=0.0.0.0/0",
		"jc=5", "jmin=30", "jmax=512", "s1=40", "s2=50", "h1=123456", "h2=234567",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("в конфигурации нет %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "allowed_ip=::/0") {
		t.Fatal("IPv6 попасть в AllowedIPs не должен")
	}
}

// testKey — ключ из повторяющегося байта в base64.
func testKey(b byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}
