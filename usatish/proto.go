package main

// Протокол MTP/1-3: рукопожатие, шифрование и разбор сообщений.
// Совпадает с server/mtp.py и с Android-клиентом (Protocol.kt).

import (
	"bytes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	tHello   = 0x01
	tWelcome = 0x02
	tReject  = 0x03
	tData    = 0x04

	kIP        = 0x00
	kKeepalive = 0x01
	kStatus    = 0x02
	kKick      = 0x03

	helloLen   = 98
	welcomeLen = 96
	rejectLen  = 18
	hdrLen     = 13
	minDataLen = 30
)

var (
	lblHello   = []byte("mtp1-hello")
	lblWelcome = []byte("mtp1-welcome")
	lblReject  = []byte("mtp1-reject")
	lblKeys    = []byte("mtp1 keys")
)

// ErrNoAnswer — сервер не ответил на рукопожатие.
var ErrNoAnswer = errors.New("сервер не отвечает: проверьте адрес и порт")

// RejectError — сервер отказал (ключ отключён, истёк или исчерпан лимит).
type RejectError struct{ Reason int }

func (e *RejectError) Error() string {
	return "сервер не пустил: " + reasonText(e.Reason)
}

// KickError — сервер отключил ключ во время сеанса.
type KickError struct{ Reason int }

func (e *KickError) Error() string {
	return "сервер отключил ключ: " + reasonText(e.Reason)
}

func reasonText(code int) string {
	switch code {
	case 1:
		return "ключ отключён администратором"
	case 2:
		return "срок действия ключа истёк"
	case 3:
		return "лимит трафика исчерпан"
	}
	return "причина не указана"
}

func tag16(key []byte, label []byte, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(label)
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)[:16]
}

func nonce12(ctr uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], ctr)
	return n
}

// replayWindow — окно защиты от повторов на 64 пакета (как на сервере).
type replayWindow struct {
	init bool
	top  uint64
	bits uint64
}

func (r *replayWindow) ok(ctr uint64) bool {
	if !r.init || ctr > r.top {
		return true
	}
	d := r.top - ctr
	return d < 64 && (r.bits>>d)&1 == 0
}

func (r *replayWindow) mark(ctr uint64) {
	switch {
	case !r.init:
		r.init, r.top, r.bits = true, ctr, 1
	case ctr > r.top:
		d := ctr - r.top
		if d < 64 {
			r.bits = (r.bits << d) | 1
		} else {
			r.bits = 1
		}
		r.top = ctr
	default:
		d := r.top - ctr
		if d < 64 {
			r.bits |= 1 << d
		}
	}
}

// Session — установленная сессия: адрес в туннеле, DNS, MTU и ключи шифрования.
type Session struct {
	SID [4]byte
	IP  net.IP
	DNS net.IP
	MTU int

	mu    sync.Mutex
	tx    cipher.AEAD
	rx    cipher.AEAD
	txCtr uint64
	rep   replayWindow
}

// HandshakeWithClock выполняет HELLO/WELCOME, добавляя к локальным часам поправку skew.
// Сервер отбрасывает HELLO, если его время расходится с серверным больше чем на HELLO_SKEW
// (120 с), и молчит — снаружи это выглядит как «handshake failed» при рабочем SSH.
func HandshakeWithClock(key *ServerKey, link Link, skew time.Duration) (*Session, error) {
	return handshake(key, link, skew)
}

// Handshake выполняет HELLO/WELCOME поверх готового канала.
func Handshake(key *ServerKey, link Link) (*Session, error) {
	return handshake(key, link, 0)
}

func handshake(key *ServerKey, link Link, skew time.Duration) (*Session, error) {
	// Для TCP первый WELCOME может прийти позже двух секунд (мобильная сеть, помехи).
	// Даём больше времени, чтобы не повторять HELLO без нужды.
	attempts, window := 5, 2*time.Second
	if _, ok := link.(*tcpLink); ok {
		attempts, window = 3, 6*time.Second
	}
	for attempt := 0; attempt < attempts; attempt++ {
		priv, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		cpub := priv.PublicKey().Bytes()
		hello := make([]byte, helloLen)
		hello[0] = tHello
		copy(hello[1:66], cpub)
		copy(hello[66:74], key.UID)
		binary.BigEndian.PutUint64(hello[74:82], uint64(time.Now().Add(skew).Unix()))
		copy(hello[82:98], tag16(key.Secret, lblHello, hello[:82]))
		if err := link.Send(hello); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(window)
		for time.Now().Before(deadline) {
			f, err := link.Recv(time.Until(deadline))
			if errors.Is(err, ErrTimeout) {
				break
			}
			if err != nil {
				return nil, err
			}
			switch {
			case len(f) == rejectLen && f[0] == tReject:
				if hmac.Equal(tag16(key.Secret, lblReject, cpub, f[:2]), f[2:18]) {
					return nil, &RejectError{Reason: int(f[1])}
				}
			case len(f) == welcomeLen && f[0] == tWelcome:
				s, ok, err := acceptWelcome(key, priv, cpub, f)
				if err != nil {
					return nil, err
				}
				if ok {
					return s, nil
				}
			}
		}
	}
	return nil, ErrNoAnswer
}

func acceptWelcome(key *ServerKey, priv *ecdh.PrivateKey, cpub, f []byte) (*Session, bool, error) {
	if !hmac.Equal(tag16(key.Secret, lblWelcome, cpub, f[:80]), f[80:96]) {
		return nil, false, nil
	}
	spub := f[15:80]
	peer, err := ecdh.P256().NewPublicKey(spub)
	if err != nil {
		return nil, false, err
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, false, err
	}
	ikm := append(append([]byte{}, shared...), key.Secret...)
	salt := append(append([]byte{}, cpub...), spub...)
	okm := make([]byte, 64)
	if _, err := io.ReadFull(hkdf.New(sha256.New, ikm, salt, lblKeys), okm); err != nil {
		return nil, false, err
	}
	tx, err := chacha20poly1305.New(okm[:32])
	if err != nil {
		return nil, false, err
	}
	rx, err := chacha20poly1305.New(okm[32:])
	if err != nil {
		return nil, false, err
	}
	s := &Session{
		IP:  net.IP(append([]byte{}, f[5:9]...)),
		DNS: net.IP(append([]byte{}, f[9:13]...)),
		MTU: int(binary.BigEndian.Uint16(f[13:15])),
		tx:  tx,
		rx:  rx,
	}
	copy(s.SID[:], f[1:5])
	return s, true, nil
}

// Encrypt шифрует один пакет и возвращает DATA-кадр: [0x04][sid][ctr][ChaCha20-Poly1305(вид ‖ данные)].
func (s *Session) Encrypt(kind byte, payload []byte) []byte {
	s.mu.Lock()
	ctr := s.txCtr
	s.txCtr++
	s.mu.Unlock()
	hdr := make([]byte, hdrLen)
	hdr[0] = tData
	copy(hdr[1:5], s.SID[:])
	binary.BigEndian.PutUint64(hdr[5:13], ctr)
	plain := make([]byte, 1+len(payload))
	plain[0] = kind
	copy(plain[1:], payload)
	out := make([]byte, hdrLen, hdrLen+len(plain)+s.tx.Overhead())
	copy(out, hdr)
	return s.tx.Seal(out, nonce12(ctr), plain, hdr)
}

// Decrypt разбирает DATA-кадр от сервера. ok=false — кадр не нужен или испорчен.
func (s *Session) Decrypt(f []byte) (byte, []byte, bool) {
	if len(f) < minDataLen || f[0] != tData || !bytes.Equal(f[1:5], s.SID[:]) {
		return 0, nil, false
	}
	ctr := binary.BigEndian.Uint64(f[5:13])
	if !s.rep.ok(ctr) {
		return 0, nil, false
	}
	plain, err := s.rx.Open(nil, nonce12(ctr), f[13:], f[:13])
	if err != nil {
		return 0, nil, false
	}
	s.rep.mark(ctr)
	if len(plain) == 0 {
		return 0, nil, false
	}
	return plain[0], plain[1:], true
}

// serverStatus — данные сервера из STATUS: сколько израсходовано, лимит и срок (0 — без лимита или срока).
type serverStatus struct {
	At      int64 `json:"-"` // момент получения STATUS, мс от 1970 (заполняет клиент)
	Used    int64 `json:"used"`
	Limit   int64 `json:"limit"`
	Expires int64 `json:"expires"`
}

func parseStatus(payload []byte) (serverStatus, bool) {
	var st serverStatus
	if err := json.Unmarshal(payload, &st); err != nil {
		return st, false
	}
	return st, true
}
