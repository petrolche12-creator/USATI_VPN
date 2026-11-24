package core

// SSH клиента: AmneziaWG-туннель из .conf → SSH → MTP-шлюз на localhost хоста.
//
// MTP-VPN слушает только 127.0.0.1 на хосте, а SSH открыт наружу. Поэтому сначала поднимается
// туннель AmneziaWG, поверх него идёт SSH, а уже в SSH открывается канал до 127.0.0.1:18443.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// netConn — соединение, которое отдаёт транспорт (обычно tcpConn поверх туннеля AmneziaWG).
type netConn = net.Conn

// sshTarget — куда и как подключаться по SSH.
type sshTarget struct {
	Host     string
	Port     int
	User     string
	Pass     string
	PinnedFP string
}

// sshDial подключается по паролю (и keyboard-interactive — так пускают многие VPS).
// Отпечаток ключа сервера проверяется, если он запомнен; при первом входе — запоминается.
func sshDial(t sshTarget) (client *ssh.Client, seen string, err error) {
	return sshDialConn(t, func() (netConn, error) {
		return net.DialTimeout("tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)), 20*time.Second)
	}, nil)
}

// sshDialConn — то же, но транспорт задаёт вызывающий (например, dial через AmneziaWG).
func sshDialConn(t sshTarget, dial func() (netConn, error), onKey func(fp string)) (*ssh.Client, string, error) {
	seen := ""
	cfg := &ssh.ClientConfig{
		User: t.User,
		Auth: []ssh.AuthMethod{
			ssh.Password(t.Pass),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = t.Pass
				}
				return answers, nil
			}),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(key)
			seen = fp
			if t.PinnedFP != "" && t.PinnedFP != fp {
				return errors.New("ключ сервера изменился. Если сервер переустанавливали, нажмите «Забыть сервер» и настройте его заново")
			}
			if onKey != nil {
				onKey(fp)
			}
			return nil
		},
		Timeout: 20 * time.Second,
	}
	conn, err := dial()
	if err != nil {
		return nil, "", err
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(t.Host, strconv.Itoa(t.Port)), cfg)
	if err != nil {
		conn.Close()
		return nil, "", err
	}
	return ssh.NewClient(c, chans, reqs), seen, nil
}

// sshResult — итог выполнения команды на сервере.
type sshResult struct {
	Code   int
	Stdout string
	Stderr string
}

// sshRun выполняет команду. stdin (если есть) уходит команде, onLine получает строки вывода по мере появления.
func sshRun(c *ssh.Client, cmd string, stdin []byte, onLine func(string)) (sshResult, error) {
	s, err := c.NewSession()
	if err != nil {
		return sshResult{}, err
	}
	defer s.Close()
	var out, errBuf strings.Builder
	errOut := &syncBuilder{w: &errBuf}
	s.Stderr = errOut
	var w io.Writer = &out
	if onLine != nil {
		w = io.MultiWriter(&out, &lineWriter{fn: onLine})
	}
	s.Stdout = w
	in, err := s.StdinPipe()
	if err != nil {
		return sshResult{}, err
	}
	if err := s.Start(cmd); err != nil {
		return sshResult{}, err
	}
	if stdin != nil {
		_, _ = in.Write(stdin)
	}
	_ = in.Close()
	res := sshResult{}
	if err := s.Wait(); err != nil {
		var ee *ssh.ExitError
		if !errors.As(err, &ee) {
			return sshResult{}, err
		}
		res.Code = ee.ExitStatus()
	}
	res.Stdout = out.String()
	res.Stderr = errBuf.String()
	return res, nil
}

type syncBuilder struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// lineWriter отдаёт строки (без переноса) функции fn по мере поступления.
type lineWriter struct {
	buf []byte
	fn  func(string)
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := strings.IndexByte(string(l.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(l.buf[:i]), "\r")
		l.buf = l.buf[i+1:]
		l.fn(line)
	}
	return len(p), nil
}

// sshForward — TCP-канал через SSH до адреса на хосте (host:port — как видит сервер).
// Канал выглядит как обычная SSH-сессия; MTP-трафик внутри неё зашифрован дополнительно.
func sshForward(c *ssh.Client, host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := c.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH-канал до %s не открылся: %w", addr, err)
	}
	return conn, nil
}

// detectGatewayPort читает порт MTP-шлюза из /opt/mtp/config.json на сервере.
// Если файла нет или он не читается, возвращается стандартный 18443.
func detectGatewayPort(c *ssh.Client) int {
	const fallback = 18443
	res, err := sshRun(c, "cat /opt/mtp/config.json 2>/dev/null", nil, nil)
	if err != nil || res.Code != 0 {
		return fallback
	}
	if i := strings.Index(res.Stdout, `"ssh_gateway_port"`); i >= 0 {
		rest := res.Stdout[i+len(`"ssh_gateway_port"`):]
		rest = strings.TrimLeft(rest, " :\"")
		var digits strings.Builder
		for _, r := range rest {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			} else {
				break
			}
		}
		if n, err := strconv.Atoi(digits.String()); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return fallback
}
