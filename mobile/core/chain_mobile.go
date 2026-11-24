package core

// Сборка всей цепочки для Android: [.conf Amnezia] → AmneziaWG → SSH → MTP-VPN.
// Kotlin вызывает три метода: Start (поднять туннель и MTP), SendPacket/RecvPacket
// (пакеты из системного VPN) и Stop. Все ошибки — обычные исключения Go → Java.

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// SshConfig — данные SSH к своему серверу (то, что пользователь вводит в настройке).
type SshConfig struct {
	Host     string
	Port     int
	User     string
	Pass     string
	PinnedFP string
}

// ChainOptions — всё, что нужно для подключения.
type ChainOptions struct {
	ConfText  string // текст .conf из Amnezia (из файла «Поделиться»)
	KeyText   string // ключ mtp://
	Transport string // "MTP2" или "MTP3" внутри SSH
	AsVpn     bool   // весь трафик в туннель Amnezia (иначе только MTP)
}

// Chain — поднятая цепочка.
type Chain struct {
	tunnel *MobileTunnel
	mtp    *MtpSession
	logger Logger

	lastTx atomic.Int64
	closed atomic.Bool
	mu     sync.Mutex
}

// startChain поднимает цепочку целиком: AmneziaWG из .conf, SSH через него и MTP поверх SSH.
func startChain(opts ChainOptions, ssh SshConfig, logger Logger) (*Chain, error) {
	log := func(f string, args ...any) {
		if logger != nil {
			logger.Log(sprintf(f, args...))
		}
	}
	if len(opts.ConfText) == 0 {
		return nil, errors.New("не загружен файл .conf Amnezia: нажмите «Добавить Amnezia» и выберите файл из приложения Amnezia")
	}
	if ssh.Host == "" || ssh.Pass == "" {
		return nil, errors.New("не заполнены данные SSH к серверу: IP, пользователь и пароль")
	}
	if ssh.Port <= 0 {
		ssh.Port = 22
	}
	log("шаг 1 из 3: поднимаю AmneziaWG по файлу .conf")
	tunnel, err := StartAWG(opts.ConfText, logger)
	if err != nil {
		return nil, errors.New("AmneziaWG не поднялся: " + err.Error())
	}
	owner := &Owner{Host: ssh.Host, SSHPort: ssh.Port, SSHUser: ssh.User, SSHPass: ssh.Pass, HostFP: ssh.PinnedFP}
	log("шаг 2 из 3: SSH на %s", ssh.Host)
	log("шаг 3 из 3: MTP через SSH-канал")
	mtp, err := StartMTPOverSSH(tunnel, owner, opts.KeyText, opts.Transport, logger, nil)
	if err != nil {
		tunnel.Stop()
		return nil, errors.New("MTP через SSH не поднялся: " + err.Error())
	}
	c := &Chain{tunnel: tunnel, mtp: mtp, logger: logger}
	c.lastTx.Store(time.Now().Unix())
	return c, nil
}

// Addr — адрес телефона в MTP-туннеле (его служба ставит на интерфейс).
func (c *Chain) Addr() string { return c.mtp.Addr() }

// DNS — DNS внутри туннеля.
func (c *Chain) DNS() string { return c.mtp.DNS() }

// MTU — MTU туннеля.
func (c *Chain) MTU() int { return c.mtp.MTU() }

// SendPacket — пакет из системного VPN на сервер.
func (c *Chain) SendPacket(p []byte) error {
	if c.closed.Load() {
		return errors.New("туннель уже закрыт")
	}
	c.lastTx.Store(time.Now().Unix())
	return c.mtp.SendPacket(p)
}

// RecvPacket — следующий пакет с сервера; nil, если за timeoutMs ничего не пришло.
func (c *Chain) RecvPacket(timeoutMs int, l StatusListener) ([]byte, error) {
	if c.closed.Load() {
		return nil, errors.New("туннель уже закрыт")
	}
	p, err := c.mtp.RecvPacket(timeoutMs, l)
	if err != nil {
		return nil, err
	}
	if p != nil {
		return p, nil
	}
	// Раз в 20 секунд — служебный кадр: сервер не должен считать сессию мёртвой.
	if now := time.Now().Unix(); now-c.lastTx.Load() >= int64(keepaliveEvery/time.Second) {
		if err := c.mtp.Keepalive(); err == nil {
			c.lastTx.Store(now)
		}
	}
	return nil, nil
}

// Rx — сколько получено через MTP (байты).
func (c *Chain) Rx() int64 { r, _ := c.mtp.Stats(); return r }

// Tx — сколько отправлено через MTP (байты).
func (c *Chain) Tx() int64 { _, t := c.mtp.Stats(); return t }

// AwgRx — трафик первой ступени (AmneziaWG): получено.
func (c *Chain) AwgRx() int64 { return c.tunnel.Rx() }

// AwgTx — трафик первой ступени (AmneziaWG): отправлено.
func (c *Chain) AwgTx() int64 { return c.tunnel.Tx() }

// Stop опускает всё: MTP, SSH и AmneziaWG. Повторный вызов безопасен.
func (c *Chain) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Swap(true) {
		return
	}
	_ = c.mtp.Close()
	c.tunnel.Stop()
	if c.logger != nil {
		c.logger.Log("туннель остановлен")
	}
}
