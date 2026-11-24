package main

// Открытие SSH-соединения до сервера из настроек. Если загружен .conf Amnezia, соединение идёт
// через AmneziaWG-туннель: снаружи виден только Amnezia-трафик, а SSH-порт хоста нигде не светится.
// Если конфиг ещё не загружен, используется прямой SSH (так работает первая настройка сервера).

import (
	"net"

	"golang.org/x/crypto/ssh"
)

// openServerSSH подключается к серверу из настроек: через AmneziaWG, если он загружен, иначе напрямую.
// direct — не использовать AmneziaWG даже при загруженном конфиге (первая установка MTP).
func openServerSSH(o *Owner, logf func(string, ...any), direct bool) (*ssh.Client, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cfg := activeAwg()
	if direct {
		cfg = nil
	}
	if cfg == nil {
		c, fp, err := sshDial(o.target())
		if err == nil && o.HostFP == "" && fp != "" {
			o.HostFP = fp
			_ = SaveOwner(defaultDataDir(), o)
		}
		return c, err
	}
	logf("SSH на %s через AmneziaWG", o.Host)
	awg, err := awgStarter(cfg, false, logf)
	if err != nil {
		return nil, err
	}
	c, _, err := sshDialConn(o.target(), func() (net.Conn, error) {
		conn, err := awg.DialTCP(o.Host, o.SSHPort)
		if err != nil {
			return nil, err
		}
		return &awgConn{Conn: conn, awg: awg}, nil
	}, func(fp string) {
		if o.HostFP == "" {
			o.HostFP = fp
			_ = SaveOwner(defaultDataDir(), o)
		}
	})
	if err != nil {
		awg.Stop()
		return nil, err
	}
	return c, nil
}

// awgConn закрывает AmneziaWG вместе с последним соединением в нём.
type awgConn struct {
	net.Conn
	awg *awgTunnel
}

func (c *awgConn) Close() error {
	err := c.Conn.Close()
	c.awg.Stop()
	return err
}
