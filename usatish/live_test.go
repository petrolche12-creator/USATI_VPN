package main

// Живые проверки против работающего сервера mtp.py на Linux (нужен root: создаётся TUN-адаптер).
// Запуск: клиент в отдельном сетевом пространстве (сервер и клиент на одной машине иначе видят друг друга напрямую):
//   sudo ip netns exec usatyi-live env USATYI_TEST_KEY='mtp://...' USATYI_TEST_HOST=10.99.0.1 ./usatyi.test -test.run Live -test.v
// Без ключа или без root тесты пропускаются.

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func liveKey(t *testing.T) *ServerKey {
	t.Helper()
	text := os.Getenv("USATYI_TEST_KEY")
	if text == "" {
		t.Skip("нет USATYI_TEST_KEY")
	}
	if os.Geteuid() != 0 {
		t.Skip("нужен root (TUN-адаптер)")
	}
	k, err := ParseKey(text)
	if err != nil {
		t.Fatal(err)
	}
	if h := os.Getenv("USATYI_TEST_HOST"); h != "" {
		k.Host = h // сервер на другом адресе (например, из сетевого пространства проверок)
	}
	return k
}

// udpProbe шлёт UDP на закрытый порт: ICMP «порт недоступен» в ответ доказывает, что UDP-пакет дошёл.
func udpProbe(addr string) string {
	conn, err := net.DialTimeout("udp4", addr, 3*time.Second)
	if err != nil {
		return "dial: " + err.Error()
	}
	defer conn.Close()
	local := conn.LocalAddr().String()
	_, _ = conn.Write([]byte("probe"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	if err == nil {
		return "ответ от " + addr + " (локальный адрес " + local + ")"
	}
	if strings.Contains(err.Error(), "refused") {
		return "ICMP «порт недоступен» (локальный адрес " + local + ")"
	}
	return "таймаут: " + err.Error() + " (локальный адрес " + local + ")"
}

// dnsThroughTunnel — DNS-запрос к 1.1.1.1 из программы: проверяет путь через туннель и NAT сервера.
func dnsThroughTunnel() bool {
	conn, err := net.DialTimeout("udp4", "1.1.1.1:53", 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	q := []byte{0x42, 0x42, 0x01, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split("example.com", ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 1, 0, 1)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(q); err != nil {
		return false
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	return err == nil && n > 12 && binary.BigEndian.Uint16(buf[:2]) == 0x4242
}

func pingOK(target string) bool {
	return exec.Command("ping", "-c", "2", "-W", "2", target).Run() == nil
}

func runMTPLive(t *testing.T, key *ServerKey, tr Transport, adapter string) {
	t.Helper()
	run, err := startMTP(runOpts{
		Key: key, Transport: tr, Routing: RAll, Adapter: adapter,
		TestRoutes: []string{"10.8.0.1/32", "1.1.1.1/32"},
	})
	if err != nil {
		t.Fatalf("%s: %v", tr.Label(), err)
	}
	defer func() {
		run.Stop()
		if err := run.Wait(); err != nil {
			t.Logf("%s: остановка с ошибкой: %v", tr.Label(), err)
		}
	}()
	if !pingOK("10.8.0.1") {
		t.Fatalf("%s: пинг 10.8.0.1 через туннель не прошёл", tr.Label())
	}
	t.Logf("%s: проба UDP к серверу: %s", tr.Label(), udpProbe("10.8.0.1:9"))
	rx0, tx0 := run.Stats()
	dnsOK := dnsThroughTunnel()
	rx1, tx1 := run.Stats()
	t.Logf("%s: DNS: ok=%v, rx %d->%d, tx %d->%d", tr.Label(), dnsOK, rx0, rx1, tx0, tx1)
	if !dnsOK {
		t.Fatalf("%s: DNS 1.1.1.1 через туннель не прошёл", tr.Label())
	}
	rx, tx := run.Stats()
	if rx == 0 || tx == 0 {
		t.Fatalf("%s: счётчики не растут: rx=%d tx=%d", tr.Label(), rx, tx)
	}
	st, ok := run.Status()
	if !ok {
		t.Logf("%s: STATUS от сервера ещё не пришёл", tr.Label())
	} else {
		t.Logf("%s: сервер: использовано %d байт", tr.Label(), st.Used)
	}
	t.Logf("%s: ок, rx=%d tx=%d", tr.Label(), rx, tx)
}

func TestLiveMTP1UDP(t *testing.T) {
	runMTPLive(t, liveKey(t), TMTP1, "usyt1")
}

func TestLiveMTP2TCP(t *testing.T) {
	runMTPLive(t, liveKey(t), TMTP2, "usyt2")
}

func TestLiveMTP3Mask(t *testing.T) {
	runMTPLive(t, liveKey(t), TMTP3, "usyt3")
}

// TestLiveChain проверит полную цепочку .conf Amnezia → AmneziaWG → SSH → MTP.
// Окружение: USATYI_TEST_AWG (путь к .conf), USATYI_TEST_SSH (user:pass@host:port),
// USATYI_TEST_KEY (строка mtp://) и root (создаются адаптеры WinTun/TUN).
func TestLiveChain(t *testing.T) {
	confPath := os.Getenv("USATYI_TEST_AWG")
	sshSpec := os.Getenv("USATYI_TEST_SSH")
	if confPath == "" || sshSpec == "" {
		t.Skip("нет USATYI_TEST_AWG или USATYI_TEST_SSH")
	}
	if os.Geteuid() != 0 {
		t.Skip("нужен root (TUN-адаптер)")
	}
	key := liveKey(t)
	raw, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseAwgConf(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var user, pass, host string
	var port int
	at := strings.LastIndex(sshSpec, "@")
	if at < 0 {
		t.Fatal("USATYI_TEST_SSH: нужен вид user:pass@host:port")
	}
	cred, addr := sshSpec[:at], sshSpec[at+1:]
	colon := strings.Index(cred, ":")
	if colon < 0 {
		t.Fatal("USATYI_TEST_SSH: нужен вид user:pass@host:port")
	}
	user, pass = cred[:colon], cred[colon+1:]
	lastColon := strings.LastIndex(addr, ":")
	host = addr[:lastColon]
	if _, err := fmt.Sscanf(addr[lastColon+1:], "%d", &port); err != nil {
		t.Fatal(err)
	}
	run, err := startChain(chainOpts{
		Cfg:    cfg,
		Server: &Owner{Host: host, SSHPort: port, SSHUser: user, SSHPass: pass},
		Key:    key,
		Run:    runOpts{Key: key, Transport: TMTP2, Routing: RAll, Adapter: "usytc"},
		Logf:   t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Stop()
	deadline := time.Now().Add(20 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		if pingOK("10.8.0.1") {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatal("пинг 10.8.0.1 через цепочку не прошёл")
	}
	rx, tx := run.Stats()
	if rx == 0 || tx == 0 {
		t.Fatalf("счётчики не растут: rx=%d tx=%d", rx, tx)
	}
	t.Logf("цепочка работает: rx=%d tx=%d", rx, tx)
}
