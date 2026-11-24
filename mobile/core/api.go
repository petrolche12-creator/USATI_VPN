package core

// Публичный API ядра для Android (собирается gomobile bind в core.aar).
//
// Всё, что нужно приложению, — три вещи:
//   1. StartChain(paramsJSON, logger) — поднять AmneziaWG из .conf и MTP через SSH;
//   2. SendPacket/RecvPacket — передать пакеты системного VPN;
//   3. Stop — опустить всё.
// Нарочно только строки, числа и байты: так gomobile делает простые Java-классы без сюрпризов.

import (
	"encoding/json"
	"errors"
)

// Logger — журнал для приложения (может быть nil).
type Logger interface {
	Log(line string)
}

// ChainParams — всё, что присылает приложение при подключении (JSON).
//
//	{
//	  "conf": "текст .conf из Amnezia",
//	  "key": "mtp://…",
//	  "transport": "MTP2" | "MTP3",
//	  "asVpn": false,
//	  "ssh": {"host": "1.2.3.4", "port": 22, "user": "root", "pass": "…", "fp": ""}
//	}
type ChainParams struct {
	Conf      string    `json:"conf"`
	Key       string    `json:"key"`
	Transport string    `json:"transport"`
	AsVpn     bool      `json:"asVpn"`
	Ssh       SshParams `json:"ssh"`
}

// SshParams — доступ по SSH к своему серверу.
type SshParams struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
	FP   string `json:"fp"`
}

// StartChain поднимает всю цепочку: AmneziaWG по .conf → SSH → MTP-VPN.
// Тяжёлая операция: вызывать из фонового потока, а не из главного.
func StartChain(paramsJSON string, logger Logger) (*Chain, error) {
	var p ChainParams
	if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil {
		return nil, errors.New("ядро не разобрало параметры подключения: " + err.Error())
	}
	opts := ChainOptions{ConfText: p.Conf, KeyText: p.Key, Transport: p.Transport, AsVpn: p.AsVpn}
	ssh := SshConfig{Host: p.Ssh.Host, Port: p.Ssh.Port, User: p.Ssh.User, Pass: p.Ssh.Pass, PinnedFP: p.Ssh.FP}
	return startChain(opts, ssh, logger)
}

// CheckConf проверяет .conf и возвращает описание для экрана (не подключаясь).
func CheckConf(text string) (string, error) {
	cfg, err := ParseAwgConf(text)
	if err != nil {
		return "", err
	}
	out := cfg.Kind() + ": сервер " + cfg.Peer.Endpoint + ", адрес " + cfg.InterfaceAddr().String()
	if len(cfg.Iface.DNS) > 0 {
		out += ", DNS " + joinComma(cfg.Iface.DNS)
	}
	if cfg.hasObf() {
		out += " (обфускация AmneziaWG)"
	} else {
		out += " (обычный WireGuard без обфускации)"
	}
	return out, nil
}

// CheckKey разбирает ключ mtp:// и говорит, какие способы в нём есть.
func CheckKey(text string) (string, error) {
	info, err := InspectKey(text)
	if err != nil {
		return "", err
	}
	return info.Name + " · сервер " + info.Host + " · способы: " + info.Supports, nil
}

// SSLink возвращает ссылку ss:// для приложения Shadowsocks.
func SSLinkOf(text string) (string, error) { return SSLink(text) }

// Version — версия ядра.
func Version() string { return coreVersion }

func joinComma(list []string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
