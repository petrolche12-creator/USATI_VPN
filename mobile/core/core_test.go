package core

// Тесты мобильного ядра: разбор .conf Amnezia, ключей и API, который видит Android.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

const testConf = `[Interface]
PrivateKey = AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=
Address = 10.8.1.5/32
DNS = 1.1.1.1, 8.8.8.8
MTU = 1420
Jc = 4
Jmin = 40
Jmax = 70
S1 = 30
S2 = 40
H1 = 123456
H2 = 223456
H3 = 323456
H4 = 423456

[Peer]
PublicKey = ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

func TestCheckConfText(t *testing.T) {
	line, err := CheckConf(testConf)
	if err != nil {
		t.Fatalf("CheckConf: %v", err)
	}
	for _, want := range []string{"vpn.example.com:51820", "10.8.1.5", "DNS 1.1.1.1, 8.8.8.8", "AmneziaWG"} {
		if !strings.Contains(line, want) {
			t.Errorf("в описании нет %q: %s", want, line)
		}
	}
}

func TestCheckConfRejectsGarbage(t *testing.T) {
	if _, err := CheckConf("просто текст"); err == nil {
		t.Fatal("мусор должен быть отвергнут")
	}
}

func TestStartChainNeedsConf(t *testing.T) {
	_, err := StartChain(`{"conf":"","ssh":{"host":"1.2.3.4","pass":"x"}}`, nil)
	if err == nil || !strings.Contains(err.Error(), "Amnezia") {
		t.Fatalf("ожидалась жалоба про .conf, получили %v", err)
	}
}

func TestStartChainNeedsSSH(t *testing.T) {
	_, err := StartChain(`{"conf":"x","ssh":{"host":"","pass":""}}`, nil)
	if err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("ожидалась жалоба про SSH, получили %v", err)
	}
}

func TestStartChainBadJSON(t *testing.T) {
	_, err := StartChain(`{`, nil)
	if err == nil || !strings.Contains(err.Error(), "параметры") {
		t.Fatalf("ожидалась жалоба про параметры, получили %v", err)
	}
}

func TestCheckKey(t *testing.T) {
	key := testKeyJSON(t)
	line, err := CheckKey(key)
	if err != nil {
		t.Fatalf("CheckKey: %v", err)
	}
	if !strings.Contains(line, "сервер") {
		t.Errorf("описание ключа без сервера: %s", line)
	}
	ss, err := SSLinkOf(key)
	if err != nil {
		t.Fatalf("SSLinkOf: %v", err)
	}
	if !strings.HasPrefix(ss, "ss://") {
		t.Errorf("ссылка Shadowsocks должна начинаться с ss://, получили %.20s", ss)
	}
}

// testKeyJSON — ключ mtp:// в том же формате, что выдаёт сервер.
func testKeyJSON(t *testing.T) string {
	t.Helper()
	uid := make([]byte, 8)
	secret := make([]byte, 32)
	b, _ := json.Marshal(map[string]any{
		"v": 2, "n": "Тест", "h": "203.0.113.5",
		"u": hex.EncodeToString(uid), "s": base64.RawURLEncoding.EncodeToString(secret),
		"p":  map[string]int{"udp": 443, "tcp": 8443, "mask": 443, "wg": 51820, "ss": 20001},
		"ss": map[string]string{"m": "chacha20-ietf-poly1305", "k": "пароль"},
	})
	return "mtp://" + base64.RawURLEncoding.EncodeToString(b)
}

func TestVersion(t *testing.T) {
	if Version() == "" {
		t.Fatal("пустая версия ядра")
	}
}

// TestAWGStopIsSafeOnceAndManyTimes — регрессия на падение ядра при подключении.
//
// Раньше ядро падало с "panic: close of closed channel": amneziawg-go сам закрывает
// tun-устройство, а startChain вызывал tunDev.Close() ещё раз при ошибке. На Android
// это выглядело как "Ядро завершилось при запуске". Теперь Stop безопасен и
// вызывается сколько угодно раз.
func TestAWGStopIsSafeOnceAndManyTimes(t *testing.T) {
	conf := "[Interface]\nPrivateKey = " + base64.StdEncoding.EncodeToString(make([]byte, 32)) +
		"\nAddress = 10.8.1.2/32\n\n[Peer]\nPublicKey = " +
		base64.StdEncoding.EncodeToString(bytes32()) + "\nEndpoint = 127.0.0.1:51999\nAllowedIPs = 0.0.0.0/0\n"

	tun, err := StartAWG(conf, nil)
	if err != nil {
		t.Skipf("AmneziaWG не поднялся в этой среде: %v", err)
	}
	// Первая и повторные остановки не должны паниковать.
	tun.Stop()
	tun.Stop()
	tun.Stop()

	// После Stop статистика не падает.
	_ = tun.Rx()
	_ = tun.Tx()
}

func bytes32() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 32)
	}
	return b
}
