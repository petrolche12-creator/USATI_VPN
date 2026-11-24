package core

// Каналы передачи кадров MTP: UDP (MTP/1), TCP с длиной кадра (MTP/2), TCP с записями (MTP/3).

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

// ErrTimeout — за отведённое время кадр не пришёл (это не ошибка соединения).
var ErrTimeout = errors.New("таймаут")

// ErrClosed — канал закрыт (остановлен или разорван).
var ErrClosed = errors.New("соединение закрыто")

// Link — канал, по которому идут кадры протокола.
type Link interface {
	Send(frame []byte) error
	Recv(timeout time.Duration) ([]byte, error)
	Close() error
}

const maxFrame = 2048

// udpLink — MTP/1: один кадр = одна датаграмма.
type udpLink struct {
	c   *net.UDPConn
	buf []byte
}

func dialUDP(host string, port int) (Link, error) {
	raddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	c, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return nil, err
	}
	return &udpLink{c: c, buf: make([]byte, 2048)}, nil
}

func (l *udpLink) Send(frame []byte) error {
	_, err := l.c.Write(frame)
	return err
}

func (l *udpLink) Recv(timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = time.Millisecond
	}
	_ = l.c.SetReadDeadline(time.Now().Add(timeout))
	n, err := l.c.Read(l.buf)
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			return nil, ErrClosed
		}
		// таймаут или ICMP «порт закрыт» (сервер перезапускается): ждём дальше
		return nil, ErrTimeout
	}
	out := make([]byte, n)
	copy(out, l.buf[:n])
	return out, nil
}

func (l *udpLink) Close() error { return l.c.Close() }

// tcpLink — MTP/2 (кадр с длиной) или MTP/3 (записи, похожие на TLS).
type tcpLink struct {
	c    net.Conn
	mask bool
	wmu  sync.Mutex
	rbuf []byte
	tmp  []byte
}

func dialTCP(host string, port int, mask bool) (Link, error) {
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(host, strconv.Itoa(port)), 10*time.Second)
	if err != nil {
		return nil, err
	}
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	return &tcpLink{c: c, mask: mask, tmp: make([]byte, 65536)}, nil
}

// newTCPLink — MTP/2 или MTP/3 поверх уже готового TCP-соединения (в программе это SSH-канал до
// MTP-шлюза на localhost сервера). raw — любое соединение, в том числе ssh-канал.
func newTCPLink(c net.Conn, mask bool) Link {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	if sc, ok := c.(interface{ SetNoDelay(bool) error }); ok {
		_ = sc.SetNoDelay(true)
	}
	return &tcpLink{c: c, mask: mask, tmp: make([]byte, 65536)}
}

func (l *tcpLink) Send(frame []byte) error {
	var data []byte
	if l.mask {
		d, err := encodeMask(frame)
		if err != nil {
			return err
		}
		data = d
	} else {
		data = encodeTCP(frame)
	}
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_, err := l.c.Write(data)
	return err
}

func (l *tcpLink) Recv(timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		frame, n, err := parseFrame(l.rbuf, l.mask)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			l.rbuf = l.rbuf[n:]
			return frame, nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, ErrTimeout
		}
		_ = l.c.SetReadDeadline(time.Now().Add(left))
		k, err := l.c.Read(l.tmp)
		if k > 0 {
			l.rbuf = append(l.rbuf, l.tmp[:k]...)
		}
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return nil, ErrTimeout
			}
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil, ErrClosed
			}
			return nil, err
		}
	}
}

func (l *tcpLink) Close() error { return l.c.Close() }

// encodeTCP — MTP/2: [длина 2, big-endian][кадр].
func encodeTCP(frame []byte) []byte {
	out := make([]byte, 2+len(frame))
	binary.BigEndian.PutUint16(out, uint16(len(frame)))
	copy(out[2:], frame)
	return out
}

// encodeMask — MTP/3: [0x16|0x17, 0x03, 0x03][длина 2][паддинг-длина 1][кадр][случайный паддинг].
func encodeMask(frame []byte) ([]byte, error) {
	rtype := byte(0x16)
	if frame[0] == tData {
		rtype = 0x17
	}
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	pad := int(b[0]) % 48
	body := 1 + len(frame) + pad
	out := make([]byte, 5+body)
	out[0] = rtype
	out[1], out[2] = 0x03, 0x03
	binary.BigEndian.PutUint16(out[3:5], uint16(body))
	out[5] = byte(pad)
	copy(out[6:], frame)
	if _, err := rand.Read(out[6+len(frame):]); err != nil {
		return nil, err
	}
	return out, nil
}

// parseFrame — первый кадр в буфере: (кадр, сколько байт занято, ошибка). n=0 — кадр ещё не пришёл целиком.
func parseFrame(buf []byte, mask bool) ([]byte, int, error) {
	if !mask {
		if len(buf) < 2 {
			return nil, 0, nil
		}
		n := int(binary.BigEndian.Uint16(buf[:2]))
		if n == 0 || n > maxFrame {
			return nil, 0, errors.New("неверная длина кадра")
		}
		if len(buf) < 2+n {
			return nil, 0, nil
		}
		return append([]byte{}, buf[2:2+n]...), 2 + n, nil
	}
	if len(buf) < 5 {
		return nil, 0, nil
	}
	if (buf[0] != 0x16 && buf[0] != 0x17) || buf[1] != 0x03 || buf[2] != 0x03 {
		return nil, 0, errors.New("сервер прислал не запись маскировки")
	}
	n := int(binary.BigEndian.Uint16(buf[3:5]))
	if n < 2 || n > maxFrame+64 {
		return nil, 0, errors.New("неверная длина записи")
	}
	if len(buf) < 5+n {
		return nil, 0, nil
	}
	pad := int(buf[5])
	if pad >= n-1 {
		return nil, 0, errors.New("неверный паддинг")
	}
	return append([]byte{}, buf[6:5+n-pad]...), 5 + n, nil
}
