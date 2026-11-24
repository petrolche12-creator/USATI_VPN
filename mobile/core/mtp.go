package core

// Мобильное ядро MTP: канал (UDP/TCP/маскировка) → рукопожатие → кадры для VPN-службы Android.
// Пакеты из системного VPN приходят сюда готовыми IPv4-пакетами, а обратно возвращаются так же:
// служба только читает и пишет файл интерфейса, никакой сетевой логики в Kotlin нет.

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	kindIP        = 0x00
	kindKeepalive = 0x01
	kindStatus    = 0x02
	kindKick      = 0x03

	keepaliveEvery = 20 * time.Second
	linkDeadAfter  = 90 * time.Second
)

// Logger — журнал для Kotlin (может быть nil).
// StatusListener — данные сервера: израсходовано, лимит, срок (0 — без лимита и срока).
type StatusListener interface {
	OnStatus(used, limit, expires int64)
}

// Conn — TCP-соединение через туннель AmneziaWG для Kotlin (io.ReadWriteCloser + закрытие).
type Conn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// MtpSession — поднятый MTP-туннель: отправка пакетов, приём кадров, счётчики и статус.
type MtpSession struct {
	link   Link
	sess   *Session
	logger Logger

	txmu sync.Mutex

	rx atomic.Int64
	tx atomic.Int64

	mu     sync.Mutex
	closed bool
}

// StartMTPOverSSH поднимает MTP через SSH-канал до MTP-шлюза на localhost сервера.
func StartMTPOverSSH(tunnel *MobileTunnel, owner *Owner, keyText string, transport string, logger Logger, onKey func(fp string)) (*MtpSession, error) {
	key, err := ParseKey(keyText)
	if err != nil {
		return nil, err
	}
	mask := false
	switch transport {
	case "MTP2", "MTP1", "":
		// MTP/1 (UDP) через SSH невозможен: UDP не туннелируется в этом канале.
		// Для MTP/1 оставляем MTP/2 — предупреждение уходит в журнал.
		if transport == "MTP1" && logger != nil {
			logger.Log("MTP/1 (UDP) не проходит через SSH-канал: используется MTP/2 поверх того же протокола")
		}
	case "MTP3":
		mask = true
	default:
		return nil, errors.New("неизвестный способ: " + transport)
	}
	if tunnel == nil || tunnel.awg == nil {
		return nil, errors.New("сначала поднимите туннель AmneziaWG")
	}
	if owner == nil {
		return nil, errors.New("нет данных SSH к серверу")
	}
	client, fp, err := sshDialConn(owner.target(), func() (netConn, error) {
		return dialViaAwg(tunnel.awg, owner.Host, owner.SSHPort)
	}, nil)
	if err != nil {
		return nil, err
	}
	if onKey != nil && fp != "" {
		onKey(fp)
	}
	// Порт шлюза: сначала из ключа, потом из config.json, потом известные стандартные.
	// На каждом кандидате делаем настоящее рукопожатие — «порт открыт» ничего не значит,
	// если на нём висит чужой сервис (тогда MTP молчит и получается «handshake failed»).
	fromKey := 0
	if p, ok := key.Port(TMTP2); ok {
		fromKey = p
	}
	skew := clockSkewBySSH(client)
	ports := gatewayCandidates(fromKey, readGatewayPort(client))
	sess, link, err := tryGatewayPorts(client, key, ports, skew, mask)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("MTP-рукопожатие через SSH не прошло (порты %v): %w", ports, err)
	}
	s := &MtpSession{link: link, sess: sess, logger: logger}
	s.log("MTP поднят: адрес %s, DNS %s, MTU %d", sess.IP, sess.DNS, sess.MTU)
	return s, nil
}

// Addr — адрес телефона внутри MTP-туннеля.
func (s *MtpSession) Addr() string { return s.sess.IP.String() }

// DNS — DNS-сервер внутри туннеля (шлюз хоста).
func (s *MtpSession) DNS() string { return s.sess.DNS.String() }

// MTU — MTU туннеля из WELCOME.
func (s *MtpSession) MTU() int { return s.sess.MTU }

// SendPacket шифрует IPv4-пакет и отправляет на сервер.
func (s *MtpSession) SendPacket(packet []byte) error {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return errors.New("не IPv4-пакет")
	}
	frame := s.sess.Encrypt(kindIP, packet)
	s.txmu.Lock()
	defer s.txmu.Unlock()
	if err := s.link.Send(frame); err != nil {
		return err
	}
	s.tx.Add(int64(len(packet)))
	return nil
}

// RecvPacket ждёт пакет с сервера (timeoutMs — сколько ждать). Возвращает nil, если пакетов нет.
// Статус и служебные сообщения обрабатываются здесь же.
func (s *MtpSession) RecvPacket(timeoutMs int, listener StatusListener) ([]byte, error) {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, nil
		}
		frame, err := s.link.Recv(left)
		if errors.Is(err, ErrTimeout) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		kind, payload, ok := s.sess.Decrypt(frame)
		if !ok {
			continue
		}
		switch kind {
		case kindIP:
			s.rx.Add(int64(len(payload)))
			return payload, nil
		case kindStatus:
			if listener != nil {
				used, limit, expires := statusFromJSON(payload)
				listener.OnStatus(used, limit, expires)
			}
		case kindKick:
			reason := 0
			if len(payload) > 0 {
				reason = int(payload[0])
			}
			return nil, &RejectError{Reason: reason}
		}
	}
}

// Keepalive отправляет служебный кадр, чтобы сервер не считал сессию мёртвой.
func (s *MtpSession) Keepalive() error {
	s.txmu.Lock()
	defer s.txmu.Unlock()
	return s.link.Send(s.sess.Encrypt(kindKeepalive, nil))
}

// Stats — трафик за сеанс: получено и отправлено (байты полезных данных).
func (s *MtpSession) Stats() (int64, int64) { return s.rx.Load(), s.tx.Load() }

// Rx — получено байт.
func (s *MtpSession) Rx() int64 { return s.rx.Load() }

// Tx — отправлено байт.
func (s *MtpSession) Tx() int64 { return s.tx.Load() }

// Close закрывает канал.
func (s *MtpSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.link.Close()
}

func (s *MtpSession) log(f string, args ...any) {
	if s.logger != nil {
		s.logger.Log(sprintf(f, args...))
	}
}

// parseStatus — JSON статуса от сервера: {"used":…,"limit":…,"expires":…}.
func statusFromJSON(payload []byte) (used, limit, expires int64) {
	var v struct {
		Used    int64 `json:"used"`
		Limit   int64 `json:"limit"`
		Expires int64 `json:"expires"`
	}
	if err := jsonUnmarshal(payload, &v); err != nil {
		return 0, 0, 0
	}
	return v.Used, v.Limit, v.Expires
}
