//go:build linux

package main

// Живая проверка второй ступени цепочки (MTP поверх SSH) на реальной сети:
//   сервер — netns srv: mtp.py + тестовый SSH-шлюз (TestLiveSSHGateway);
//   клиент  — netns cli: программа соединяется по SSH, открывает прямой канал до
//            127.0.0.1:18443 на сервере и поднимает MTP/2 через этот канал.
// Запускается из tests/live_netns.sh (создаёт пространства и окружение).

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func envInt(name string) int {
	n, _ := strconv.Atoi(os.Getenv(name))
	return n
}

// TestLiveSSHGateway — тестовый SSH-сервер на сервере (вместо sshd, которого нет в песочнице).
// Умеет ровно то, что нужно программе: пароль и канал direct-tcpip до localhost сервера.
func TestLiveSSHGateway(t *testing.T) {
	addr := os.Getenv("USATYI_LIVE_GATEWAY_ADDR")
	if addr == "" {
		t.Skip("нет USATYI_LIVE_GATEWAY_ADDR")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	_ = pub
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == os.Getenv("USATYI_LIVE_SSH_USER") && string(pass) == os.Getenv("USATYI_LIVE_SSH_PASS") {
				return nil, nil
			}
			return nil, io.EOF
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SSH-шлюз слушает %s", addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go serveSSHConn(c, cfg)
	}
}

// TestLiveServerStubs — DNS и HTTP на адресах шлюза (172.28.0.53 / 172.28.0.1) внутри netns сервера.
// На настоящем сервере это dnsmasq и сам хост; здесь — заглушки, чтобы проверить маршрутизацию.
func TestLiveServerStubs(t *testing.T) {
	if os.Getenv("USATYI_LIVE_STUBS") == "" {
		t.Skip("нет USATYI_LIVE_STUBS")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // DNS: отвечает пустым NXDOMAIN на любой запрос
		defer wg.Done()
		c, err := net.ListenPacket("udp", "172.28.0.53:53")
		if err != nil {
			t.Errorf("DNS-заглушка: %v", err)
			return
		}
		defer c.Close()
		buf := make([]byte, 512)
		for {
			n, from, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			reply := append(append([]byte{}, buf[:2]...), 0x81, 0x83, 0, 1, 0, 0, 0, 0, 0, 0)
			c.WriteTo(reply, from)
		}
	}()
	go func() { // HTTP: «localhost сервера» через адрес шлюза
		defer wg.Done()
		l, err := net.Listen("tcp", "172.28.0.1:8080")
		if err != nil {
			t.Errorf("HTTP-заглушка: %v", err)
			return
		}
		defer l.Close()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
			c.Close()
		}
	}()
	t.Log("заглушки хоста: DNS 172.28.0.53:53, HTTP 172.28.0.1:8080")
	wg.Wait()
}

func serveSSHConn(c net.Conn, cfg *ssh.ServerConfig) {
	defer c.Close()
	sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sconn.Close()
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "direct-tcpip" {
			newCh.Reject(ssh.UnknownChannelType, "только direct-tcpip")
			continue
		}
		var p struct {
			Host  string
			Port  uint32
			OHost string
			OPort uint32
		}
		if err := ssh.Unmarshal(newCh.ExtraData(), &p); err != nil {
			newCh.Reject(ssh.Prohibited, "битые данные канала")
			continue
		}
		target, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))), 5*time.Second)
		if err != nil {
			newCh.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			target.Close()
			continue
		}
		go ssh.DiscardRequests(chReqs)
		go func() { io.Copy(ch, target); ch.Close() }()
		go func() { io.Copy(target, ch); target.Close() }()
	}
}

// TestLiveMTPOverSSH — вся вторая ступень: SSH по сети → канал → MTP/2 → туннель.
func TestLiveMTPOverSSH(t *testing.T) {
	if os.Getenv("USATYI_LIVE_KEY") == "" {
		t.Skip("нет окружения живой проверки (см. tests/live_netns.sh)")
	}
	if os.Geteuid() != 0 {
		t.Skip("нужен root (TUN-адаптер)")
	}
	key, err := ParseKey(os.Getenv("USATYI_LIVE_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	sshHost := os.Getenv("USATYI_LIVE_SSH_HOST")
	sshPort := envInt("USATYI_LIVE_SSH_PORT")
	own := &Owner{Host: sshHost, SSHPort: sshPort,
		SSHUser: os.Getenv("USATYI_LIVE_SSH_USER"), SSHPass: os.Getenv("USATYI_LIVE_SSH_PASS"), IsRoot: true}

	client, _, err := sshDial(own.target())
	if err != nil {
		t.Fatalf("SSH: %v", err)
	}
	defer client.Close()
	// Всё как в программе: поправка часов по серверу, кандидаты портов из ключа и
	// конфига, настоящее рукопожатие на каждом кандидате.
	fromKey := 0
	if p, ok := key.Port(TMTP2); ok {
		fromKey = p
	}
	skew := clockSkewBySSH(client, func(s string) string { return s }, nil)
	t.Logf("поправка часов по серверу: %s", skew.Round(time.Second))
	ports := gatewayCandidates(fromKey, detectGatewayPort(client, func(s string) string { return s }, nil))
	sess, link, err := tryGatewayPorts(client, key, ports, skew, false)
	if err != nil {
		t.Fatalf("MTP через SSH (порты %v): %v", ports, err)
	}
	t.Logf("сессия: адрес %s, DNS %s, MTU %d", sess.IP, sess.DNS, sess.MTU)
	if sess.DNS.String() != "172.28.0.53" {
		t.Fatalf("через SSH сервер должен выдать DNS шлюза 172.28.0.53, а выдал %s", sess.DNS)
	}
	if !strings.HasPrefix(sess.IP.String(), "172.28.") {
		t.Fatalf("через SSH адрес должен быть из пула шлюза, а выдан %s", sess.IP)
	}

	defer link.Close()
	run, err := startMTPWith(runOpts{
		Key: key, Transport: TMTP2, Routing: RAll, Adapter: "usytns",
		TestRoutes: []string{"10.8.0.1/32", "172.28.0.1/32", "172.28.0.53/32"},
	}, link, sess)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Stop()

	if !waitPing("10.8.0.1", 10*time.Second) {
		t.Fatal("пинг шлюза MTP через туннель не прошёл")
	}
	t.Log("пинг шлюза MTP 10.8.0.1: ок")

	// 172.28.0.1 — адрес шлюза на петле сервера, 172.28.0.53 — DNS-форвардер хоста.
	if !waitHTTP("http://172.28.0.1:8080/", 15*time.Second) {
		t.Fatal("HTTP через шлюз 172.28.0.1 (петля сервера) не открылся")
	}
	t.Log("HTTP к шлюзу 172.28.0.1 через туннель: ок")

	if !waitDNS("172.28.0.53:53", 10*time.Second) {
		t.Fatal("DNS-запрос на 172.28.0.53 через туннель не прошёл")
	}
	t.Log("DNS на 172.28.0.53 через туннель: ок")

	rx, tx := run.Stats()
	if rx == 0 || tx == 0 {
		t.Fatalf("счётчики не растут: rx=%d tx=%d", rx, tx)
	}
	if st, has := run.Status(); !has || st.Limit < 0 {
		t.Fatalf("STATUS от сервера не получен: %+v %v", st, has)
	}
	t.Logf("трафик: rx=%d tx=%d", rx, tx)

	// MTP/3 (маскировка) через тот же SSH-шлюз проверяем последним: серверный шлюз должен
	// сам распознать кадры TLS-записи. Раньше он ждал только MTP/2 и молчал, а клиент
	// в этом режиме получал «handshake failed».
	c3, err := sshForward(client, "127.0.0.1", ports[0])
	if err != nil {
		t.Fatalf("MTP/3: канал не открылся: %v", err)
	}
	l3 := newTCPLink(c3, true)
	s3, err := handshake(key, l3, skew)
	if err != nil {
		l3.Close()
		t.Fatalf("MTP/3 (маскировка) через SSH не прошёл: %v", err)
	}
	t.Logf("MTP/3 через SSH: адрес %s", s3.IP)
	l3.Close()
}

// TestLiveClockSkew — «handshake failed» из-за расхождения часов.
//
// Сервер отбрасывает HELLO, если время в нём отличается от серверного больше чем на
// 120 секунд, и при этом молчит. На телефоне или компьютере с убежавшими часами SSH
// работает, а рукопожатие падает. Проверяем оба факта: со «сбитым» временем не проходит,
// с поправкой по серверу (её берёт clockSkewBySSH) — проходит.
func TestLiveClockSkew(t *testing.T) {
	if os.Getenv("USATYI_LIVE_CLOCK_SKEW") == "" {
		t.Skip("нет USATYI_LIVE_CLOCK_SKEW (см. tests/live_netns.sh)")
	}
	if os.Geteuid() != 0 {
		t.Skip("нужен root (TUN-адаптер)")
	}
	key, err := ParseKey(os.Getenv("USATYI_LIVE_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	own := &Owner{
		Host: os.Getenv("USATYI_LIVE_SSH_HOST"), SSHPort: envInt("USATYI_LIVE_SSH_PORT"),
		SSHUser: os.Getenv("USATYI_LIVE_SSH_USER"), SSHPass: os.Getenv("USATYI_LIVE_SSH_PASS"), IsRoot: true,
	}
	client, _, err := sshDial(own.target())
	if err != nil {
		t.Fatalf("SSH: %v", err)
	}
	defer client.Close()

	fromKey := 0
	if p, ok := key.Port(TMTP2); ok {
		fromKey = p
	}
	ports := gatewayCandidates(fromKey, detectGatewayPort(client, func(s string) string { return s }, nil))
	run := func(skew time.Duration) error {
		conn, err := sshForward(client, "127.0.0.1", ports[0])
		if err != nil {
			return err
		}
		link := newTCPLink(conn, false)
		defer link.Close()
		_, err = handshake(key, link, skew)
		return err
	}

	// Часы «убежали» на 10 минут — сервер должен промолчать.
	if err := run(-10 * time.Minute); err == nil {
		t.Fatal("со сбитым временем рукопожатие прошло: сервер не проверяет время — тест не о том")
	} else {
		t.Logf("сбитое время отклонено сервером, как и ожидалось: %v", err)
	}

	// А теперь так, как делает программа: время берём у сервера.
	skew := clockSkewBySSH(client, func(s string) string { return s }, nil)
	if err := run(skew); err != nil {
		t.Fatalf("с поправкой часов (%s) рукопожатие не прошло: %v", skew.Round(time.Second), err)
	}
	t.Logf("поправка по серверу (%s) спасла рукопожатие", skew.Round(time.Second))
}
