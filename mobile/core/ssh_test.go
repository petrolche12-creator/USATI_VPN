package core

// Живой SSH через подменённый транспорт: проверяем, что sshDialConn ходит сквозь
// произвольный dial (так же, как он ходит через стек AmneziaWG), и что отпечаток
// ключа сервера проверяется и запоминается.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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
