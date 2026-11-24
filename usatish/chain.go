package main

// Цепочка подключения: .conf Amnezia → туннель AmneziaWG → SSH на хост → MTP-шлюз на localhost.
//
//   [.conf AmneziaWG] → [AmneziaWG-туннель] → [SSH:22 на хосте] → [127.0.0.1:18443 = MTP/2]
//
// Внутри MTP-канала работает основной туннель: MTP/1 (UDP), MTP/2 (TCP) или MTP/3 (маскировка).
// Весь путь зашифрован дважды: MTP внутри SSH внутри AmneziaWG.

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// chainOpts — всё, что нужно, чтобы поднять цепочку.
type chainOpts struct {
	Cfg       *AwgConfig // .conf Amnezia
	Server    *Owner     // SSH-доступ к хосту с MTP-VPN
	Key       *ServerKey // ключ mtp://
	Run       runOpts    // способ (MTP/1-3) и маршрутизация внутри MTP-туннеля
	RouteAll  bool       // отдать весь трафик компьютера в AmneziaWG
	Logf      func(string, ...any)
	OnHostKey func(fp string) // первый вход: сохранить отпечаток ключа сервера
}

// chainRun — поднятая цепочка: MTP-туннель + SSH-сеанс + AmneziaWG.
type chainRun struct {
	mtp   *mtpRun
	ssh   *ssh.Client
	awg   *awgTunnel
	stop  sync.Once
	done  sync.Once
	ended chan struct{}
	err   error
	errMu sync.Mutex
}

func (c *chainRun) Stop() {
	c.stop.Do(func() {
		if c.mtp != nil {
			c.mtp.Stop()
		}
		if c.ssh != nil {
			_ = c.ssh.Close()
		}
		if c.awg != nil {
			c.awg.Stop()
		}
	})
}

// Wait ждёт, пока MTP-канал завершится (обрыв SSH, ошибка сервера, отключение).
func (c *chainRun) Wait() error {
	<-c.ended
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

// Stats — суммарный трафик MTP (внутри SSH).
func (c *chainRun) Stats() (int64, int64) { return c.mtp.Stats() }

// Status — данные сервера MTP (использовано, лимит, срок).
func (c *chainRun) Status() (serverStatus, bool) { return c.mtp.Status() }

func (c *chainRun) finish(err error) {
	c.done.Do(func() {
		c.errMu.Lock()
		c.err = err
		c.errMu.Unlock()
		close(c.ended)
	})
}

// startChain поднимает цепочку и возвращает готовый туннель. Работает в фоне.
func startChain(o chainOpts) (*chainRun, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.Cfg == nil {
		return nil, errors.New("сначала загрузите файл .conf Amnezia")
	}
	if o.Server == nil {
		return nil, errors.New("сначала настройте сервер в настройках: IP, пользователь, пароль SSH и порт")
	}
	if o.Key == nil {
		return nil, errors.New("в списке нет ключа MTP: получите его на сервере («Сервер» → «Показать ключ»)")
	}
	if awgStarter == nil {
		return nil, errNoAwg
	}

	logf("шаг 1/4: поднимаю AmneziaWG (%s) по конфигу «%s»", o.Cfg.Kind(), o.Cfg.DisplayName())
	awg, err := awgStarter(o.Cfg, o.RouteAll, logf)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*chainRun, error) {
		awg.Stop()
		return nil, e
	}

	logf("шаг 2/4: SSH на %s@%s (порт %d) через туннель AmneziaWG", o.Server.SSHUser, o.Server.Host, o.Server.SSHPort)
	target := sshTarget{
		Host:     o.Server.Host,
		Port:     o.Server.SSHPort,
		User:     o.Server.SSHUser,
		Pass:     o.Server.SSHPass,
		PinnedFP: o.Server.HostFP,
	}
	client, _, err := sshDialConn(target, func() (net.Conn, error) {
		return dialViaAwg(awg, o.Server.Host, o.Server.SSHPort)
	}, func(fp string) {
		// первый вход: запоминаем ключ сервера, чтобы заметить подмену в следующий раз
		if o.Server.HostFP == "" && o.OnHostKey != nil {
			o.OnHostKey(fp)
		}
	})
	if err != nil {
		return fail(fmt.Errorf("SSH через AmneziaWG не удался (%s@%s:%d): %w", target.User, target.Host, target.Port, err))
	}
	if err := sshPing(client); err != nil {
		client.Close()
		return fail(fmt.Errorf("SSH-вход прошёл, но команда не выполнилась: %w", err))
	}
	logf("SSH-вход выполнен")

	port := detectGatewayPort(client, o.Server.wrap, o.Server.stdinFor())
	logf("шаг 3/4: открываю SSH-канал до 127.0.0.1:%d на хосте (порт MTP-VPN внутри SSH)", port)
	conn, err := sshForward(client, "127.0.0.1", port)
	if err != nil {
		client.Close()
		return fail(err)
	}
	logf("шаг 4/4: поднимаю %s через канал", o.Run.Transport.Label())

	// MTP/2 поверх готового TCP-соединения (SSH-канал).
	link := newTCPLink(conn, false)
	sess, err := Handshake(o.Key, link)
	if err != nil {
		link.Close()
		client.Close()
		return fail(fmt.Errorf("MTP-сервер через SSH не ответил: %w", err))
	}
	run, err := startMTPWith(o.Run, link, sess)
	if err != nil {
		link.Close()
		client.Close()
		return fail(err)
	}
	logf("цепочка поднята: AmneziaWG → SSH %s → %s, адрес в туннеле %s", o.Server.Host, o.Run.Transport.Label(), sess.IP)

	c := &chainRun{mtp: run, ssh: client, awg: awg, ended: make(chan struct{})}
	go func() {
		err := run.Wait()
		if err != nil {
			logf("туннель остановился: %v", err)
		}
		c.finish(err)
		_ = client.Close()
		awg.Stop()
	}()
	_ = strconv.Itoa(port)
	return c, nil
}

var _ = time.Second
