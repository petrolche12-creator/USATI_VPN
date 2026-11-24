package core

// Живой SSH через подменённый транспорт: проверяем, что sshDialConn ходит сквозь
// произвольный dial (так же, как он ходит через стек AmneziaWG), и что отпечаток
// ключа сервера проверяется и запоминается.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testSSHServer поднимает SSH-сервер на 127.0.0.1 и возвращает адрес, пароль и отпечаток.
func testSSHServer(t *testing.T) (addr string, pass string, fp string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) == "секрет" {
				return nil, nil
			}
			return nil, errBadPass
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				defer sc.Wait()
				go func() {
					for r := range reqs {
						if r.WantReply {
							_ = r.Reply(false, nil)
						}
					}
				}()
				go func() {
					for nc := range chans {
						if nc.ChannelType() != "session" {
							_ = nc.Reject(ssh.UnknownChannelType, "только session")
							continue
						}
						ch, chReqs, err := nc.Accept()
						if err != nil {
							continue
						}
						go func() {
							defer ch.Close()
							for req := range chReqs {
								switch req.Type {
								case "exec":
									if req.WantReply {
										_ = req.Reply(true, nil)
									}
									_, _ = ch.Write([]byte("привет\n"))
									_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
									return
								default:
									if req.WantReply {
										_ = req.Reply(false, nil)
									}
								}
							}
						}()
					}
				}()
			}()
		}
	}()
	return ln.Addr().String(), "секрет", ssh.FingerprintSHA256(signer.PublicKey())
}

var errBadPass = &authErr{}

type authErr struct{}

func (*authErr) Error() string { return "пароль не подошёл" }

// TestSSHDialThroughCustomTransport: SSH идёт через dial, который мы подсунули,
// и это ровно тот путь, которым пользуется мобильное ядро (через стек AmneziaWG).
func TestSSHDialThroughCustomTransport(t *testing.T) {
	addr, pass, fp := testSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	if _, err := fmtSscan(portStr, &port); err != nil {
		t.Fatal(err)
	}

	var seen string
	client, got, err := sshDialConn(sshTarget{Host: host, Port: port, User: "root", Pass: pass},
		func() (netConn, error) {
			// Как dialViaAwg: соединение из другого стека, здесь — обычный TCP.
			var d net.Dialer
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return d.DialContext(ctx, "tcp", addr)
		},
		func(f string) { seen = f })
	if err != nil {
		t.Fatalf("SSH через свой транспорт не поднялся: %v", err)
	}
	defer client.Close()
	if got != fp {
		t.Fatalf("отпечаток %q, ожидался %q", got, fp)
	}
	if seen != fp {
		t.Fatalf("onKey получил %q, ожидался %q", seen, fp)
	}

	// Команда через этот SSH работает
	res, err := sshRun(client, "echo привет", nil, nil)
	if err != nil {
		t.Fatalf("команда: %v", err)
	}
	if res.Code != 0 {
		t.Fatalf("код команды %d", res.Code)
	}
}

// TestSSHPinnedFingerprint: чужой отпечаток должен остановить подключение.
func TestSSHPinnedFingerprint(t *testing.T) {
	addr, pass, _ := testSSHServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmtSscan(portStr, &port)
	_, _, err := sshDialConn(sshTarget{Host: host, Port: port, User: "root", Pass: pass, PinnedFP: "SHA256:чужой"},
		func() (netConn, error) { return net.DialTimeout("tcp", addr, 5*time.Second) }, nil)
	if err == nil {
		t.Fatal("подключение с чужим отпечатком должно быть отвергнуто")
	}
}

// fmtSscan — маленькая замена fmt.Sscan, чтобы не тянуть fmt ради одного числа.
func fmtSscan(s string, v *int) (int, error) {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errBadPort
		}
		n = n*10 + int(r-'0')
	}
	*v = n
	return 1, nil
}

var errBadPort = &authErr{}

// serveForwardingSSH поднимает SSH-сервер с поддержкой прямых каналов (как sshd):
// клиент может открыть канал до localhost:порт на «сервере», как в настоящей цепочке.
func serveForwardingSSH(t *testing.T, password string) (addr string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if string(p) == password {
				return nil, nil
			}
			return nil, errBadPass
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	type directReq struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				defer sc.Wait()
				go func() {
					for r := range reqs {
						if r.WantReply {
							_ = r.Reply(false, nil)
						}
					}
				}()
				for nc := range chans {
					if nc.ChannelType() != "direct-tcpip" {
						_ = nc.Reject(ssh.UnknownChannelType, "только direct-tcpip")
						continue
					}
					var req directReq
					if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
						_ = nc.Reject(ssh.ConnectionFailed, "плохой запрос")
						continue
					}
					go func(nc ssh.NewChannel, req directReq) {
						target, err := net.DialTimeout("tcp",
							net.JoinHostPort(req.Host, fmtSscanStr(int(req.Port))), 3*time.Second)
						if err != nil {
							_ = nc.Reject(ssh.ConnectionFailed, err.Error())
							return
						}
						ch, chReqs, err := nc.Accept()
						if err != nil {
							target.Close()
							return
						}
						go ssh.DiscardRequests(chReqs)
						go func() { _, _ = io.Copy(target, ch); target.Close() }()
						_, _ = io.Copy(ch, target)
						ch.Close()
					}(nc, req)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// fmtSscanStr — минимальный int→string без лишних импортов в тесте.
func fmtSscanStr(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestDetectGatewayPortPicksListeningPort — регрессия на «handshake failed» на шаге 4.
//
// На сервере может быть старый config.json с одним портом, а слушать сервер будет
// другой. Клиент обязан проверить, что порт реально принимает соединение, и выбрать
// рабочий, иначе SSH-канал открывается на пустой порт и MTP-рукопожатие не проходит.
func TestDetectGatewayPortPicksListeningPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:18443")
	if err != nil {
		t.Skipf("порт 18443 занят: %v", err)
	}
	defer busy.Close()

	addr := serveForwardingSSH(t, "секрет")
	host, portStr, _ := net.SplitHostPort(addr)
	var port int
	fmtSscan(portStr, &port)

	client, _, err := sshDialConn(sshTarget{Host: host, Port: port, User: "root", Pass: "секрет"},
		func() (netConn, error) { return net.DialTimeout("tcp", addr, 5*time.Second) }, nil)
	if err != nil {
		t.Fatalf("SSH: %v", err)
	}
	defer client.Close()

	// Конфиг говорит 8443, но слушает 18443 — клиент должен найти рабочий порт.
	if got := detectGatewayPortFrom(client, 8443); got != 18443 {
		t.Fatalf("выбран порт %d, ожидался рабочий 18443", got)
	}
	if got := detectGatewayPortFrom(client, 18443); got != 18443 {
		t.Fatalf("порт из конфига рабочий, но выбран %d", got)
	}
}
