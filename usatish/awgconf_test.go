package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleAwgConf = `[Interface]
PrivateKey = YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY=
Address = 10.66.66.2/32
DNS = 1.1.1.1
Jc = 4
Jmin = 50
Jmax = 1000
S1 = 30
S2 = 40
H1 = 1234567
H2 = 2345678
H3 = 3456789
H4 = 4567890

[Peer]
PublicKey = YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXphYmNkZWY=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0
PersistentKeepalive = 25
`

func TestParseAwgConf(t *testing.T) {
	cfg, err := ParseAwgConf(sampleAwgConf)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind() != "AmneziaWG" {
		t.Fatalf("не увидел обфускацию: %s", cfg.Kind())
	}
	if cfg.Host() != "vpn.example.com" || cfg.Port() != 51820 {
		t.Fatalf("endpoint: %s %d", cfg.Host(), cfg.Port())
	}
	if cfg.InterfaceAddr().String() != "10.66.66.2" {
		t.Fatalf("адрес: %s", cfg.InterfaceAddr())
	}
	if cfg.Peer.Keepalive != 25 {
		t.Fatalf("keepalive: %d", cfg.Peer.Keepalive)
	}
	if !cfg.hasObf() {
		t.Fatal("обфускация должна быть найдена")
	}
}

func TestParseAwgConfErrors(t *testing.T) {
	cases := map[string]string{
		"нет Interface": "[Peer]\nPublicKey = " + testKey(2) + "\nEndpoint = 1.2.3.4:5\nAllowedIPs = 0.0.0.0/0\n",
		"нет Peer":      "[Interface]\nPrivateKey = " + testKey(1) + "\nAddress = 10.0.0.2/32\n",
		"битый ключ":    "[Interface]\nPrivateKey = short\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = " + testKey(2) + "\nEndpoint = 1.2.3.4:5\nAllowedIPs = 0.0.0.0/0\n",
		"нет порта":     "[Interface]\nPrivateKey = " + testKey(1) + "\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = " + testKey(2) + "\nEndpoint = 1.2.3.4\nAllowedIPs = 0.0.0.0/0\n",
	}
	for name, text := range cases {
		if _, err := ParseAwgConf(text); err == nil {
			t.Fatalf("%s: ошибка не обнаружена", name)
		}
	}
}

func TestPlainWireGuardConf(t *testing.T) {
	text := strings.ReplaceAll(strings.ReplaceAll(sampleAwgConf, "Jc = 4\nJmin = 50\nJmax = 1000\nS1 = 30\nS2 = 40\nH1 = 1234567\nH2 = 2345678\nH3 = 3456789\nH4 = 4567890\n", ""), "\r\n", "\n")
	cfg, err := ParseAwgConf(text)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind() != "WireGuard" {
		t.Fatalf("должен быть обычный WireGuard: %s", cfg.Kind())
	}
}

func TestSaveAndLoadAmnezia(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "from-amnezia.conf")
	if err := os.WriteFile(src, []byte(sampleAwgConf), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := SaveAmnezia(data, src)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DisplayName() != "from-amnezia" {
		t.Fatalf("имя: %s", cfg.DisplayName())
	}
	if activeAwg() == nil {
		t.Fatal("конфиг не активирован")
	}
	if _, err := os.Stat(filepath.Join(data, "amnezia.conf")); err != nil {
		t.Fatal(err)
	}
	if err := ForgetAmnezia(data); err != nil {
		t.Fatal(err)
	}
	if activeAwg() != nil {
		t.Fatal("конфиг не забыт")
	}
}

// Сохранённый .conf должен подхватываться при запуске программы (без повторной загрузки).
func TestAwgRestoredOnStart(t *testing.T) {
	if activeAwg() != nil {
		ForgetAmnezia(t.TempDir())
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "share.conf")
	if err := os.WriteFile(src, []byte(sampleAwgConf), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveAmnezia(dir, src); err != nil {
		t.Fatal(err)
	}
	// «перезапуск»: в памяти пусто, каталог данных тот же
	ForgetAmnezia(dir) // стирает и файл — вернём его обратно
	if err := os.WriteFile(filepath.Join(dir, "amnezia.conf"), []byte(sampleAwgConf), 0o600); err != nil {
		t.Fatal(err)
	}
	if activeAwg() != nil {
		t.Fatal("после ForgetAmnezia конфиг не должен быть активен")
	}
	app, err := NewApp(dir, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	info := app.AmneziaStatus()
	if !info.Loaded || info.Name != "amnezia" {
		t.Fatalf("конфиг не подхватился при запуске: %+v", info)
	}
}
