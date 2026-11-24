package main

// Управление сервером по SSH: установка и админ-команды mtp.py. Повторяет Admin.kt с Android.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	adminScript = "/opt/mtp/mtp.py"
	setupDir    = "$HOME/usatyi-setup"
)

// Owner — свой сервер: SSH-доступ и порт VPN.
type Owner struct {
	Host    string
	SSHPort int
	SSHUser string
	SSHPass string
	VPNPort int
	IsRoot  bool
	HostFP  string
}

func (o *Owner) target() sshTarget {
	return sshTarget{Host: o.Host, Port: o.SSHPort, User: o.SSHUser, Pass: o.SSHPass, PinnedFP: o.HostFP}
}

// wrap — команда через sudo для не-root пользователя (пароль уходит через stdin).
func (o *Owner) wrap(cmd string) string {
	if o.IsRoot {
		return cmd
	}
	return "sudo -S -p '' " + cmd
}

func (o *Owner) stdinFor() []byte {
	if o.IsRoot {
		return nil
	}
	return []byte(o.SSHPass + "\n")
}

// shQuote — аргумент для командной строки сервера (в одинарных кавычках).
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// errorText — последние осмысленные строки stderr для показа пользователю.
func errorText(r sshResult) string {
	var lines []string
	for _, l := range strings.Split(r.Stderr, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "[sudo]") {
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return fmt.Sprintf("код выхода %d", r.Code)
	}
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "\n")
}

// runAdmin запускает mtp.py на сервере и возвращает stdout.
// runAdmin выполняет команду mtp.py на сервере. Если загружен .conf Amnezia, вход идёт
// через AmneziaWG-туннель (снаружи виден только Amnezia), иначе — прямым SSH.
func runAdmin(o *Owner, args string) (string, error) {
	c, err := openServerSSH(o, nil, false)
	if err != nil {
		return "", err
	}
	defer c.Close()
	// Явно системный Python: под sudo PATH может указывать на /usr/local/bin/python3 без cryptography.
	res, err := sshRun(c, o.wrap("/usr/bin/python3 "+adminScript+" "+args), o.stdinFor(), nil)
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		return "", errors.New(errorText(res))
	}
	return res.Stdout, nil
}

// parseJSONObject разбирает первый JSON-объект из вывода (на случай лишних строк).
func parseJSONObject(out string, dst any) error {
	s := strings.Index(out, "{")
	e := strings.LastIndex(out, "}")
	if s < 0 || e < s {
		return errors.New("неожиданный ответ сервера")
	}
	return json.Unmarshal([]byte(out[s:e+1]), dst)
}

// adminUser — ключ на сервере (поля как в выводе mtp.py list --json).
type adminUser struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Status  string `json:"status"`
	Used    int64  `json:"used"`
	Limit   int64  `json:"limit"`
	Expires int64  `json:"expires"`
	Online  bool   `json:"online"`
}

type adminList struct {
	Host  string      `json:"host"`
	Port  int         `json:"port"`
	Users []adminUser `json:"users"`
}

func adminListUsers(o *Owner) (*adminList, error) {
	out, err := runAdmin(o, "list --json")
	if err != nil {
		return nil, err
	}
	var l adminList
	if err := parseJSONObject(out, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func adminAddKey(o *Owner, name string, days int, limitGB float64) (string, error) {
	out, err := runAdmin(o, fmt.Sprintf("add --name=%s --days=%d --limit-gb=%.4f --json", shQuote(name), days, limitGB))
	if err != nil {
		return "", err
	}
	var r struct {
		Key string `json:"key"`
	}
	if err := parseJSONObject(out, &r); err != nil || r.Key == "" {
		return "", errors.New("сервер не вернул ключ")
	}
	return r.Key, nil
}

func adminShowKey(o *Owner, name string) (string, error) {
	out, err := runAdmin(o, "show "+shQuote(name)+" --json")
	if err != nil {
		return "", err
	}
	var r struct {
		Key string `json:"key"`
	}
	if err := parseJSONObject(out, &r); err != nil || r.Key == "" {
		return "", errors.New("сервер не вернул ключ")
	}
	return r.Key, nil
}

func adminSetEnabled(o *Owner, name string, enabled bool) error {
	flag := "--disable"
	if enabled {
		flag = "--enable"
	}
	_, err := runAdmin(o, "set "+shQuote(name)+" "+flag+" --json")
	return err
}

func adminResetTraffic(o *Owner, name string) error {
	_, err := runAdmin(o, "set "+shQuote(name)+" --reset --json")
	return err
}

func adminDelete(o *Owner, name string) error {
	_, err := runAdmin(o, "delete "+shQuote(name)+" --json")
	return err
}

// installServer ставит сервер по SSH: загружает mtp.py и install.sh через stdin и запускает установку.
// Возвращает настройки сервера с запомненным отпечатком ключа.
func installServer(form Owner, mtpPy, installSh []byte, logf func(string)) (*Owner, error) {
	logf("SSH: вхожу на " + form.Host + "…")
	c, err := openServerSSH(&form, func(f string, a ...any) { logf(fmt.Sprintf(f, a...)) }, true)
	if err != nil {
		return nil, err
	}
	fp := form.HostFP
	defer c.Close()
	probe, err := sshRun(c, "id -u", nil, nil)
	if err != nil {
		return nil, err
	}
	if probe.Code != 0 {
		return nil, errors.New("вход по SSH не удался: " + errorText(probe))
	}
	o := form
	o.IsRoot = strings.TrimSpace(probe.Stdout) == "0"
	if o.HostFP == "" && fp != "" {
		o.HostFP = fp
	}
	if o.IsRoot {
		logf("вход под root")
	} else {
		logf("вход под " + o.SSHUser + ", использую sudo")
	}

	up1, err := sshRun(c, "mkdir -p "+setupDir+" && cat > "+setupDir+"/mtp.py", mtpPy, nil)
	if err != nil {
		return nil, err
	}
	if up1.Code != 0 {
		return nil, errors.New("загрузка mtp.py: " + errorText(up1))
	}
	up2, err := sshRun(c, "cat > "+setupDir+"/install.sh", installSh, nil)
	if err != nil {
		return nil, err
	}
	if up2.Code != 0 {
		return nil, errors.New("загрузка install.sh: " + errorText(up2))
	}
	logf("файлы загружены, запускаю установку…")

	r, err := sshRun(c, o.wrap("bash "+setupDir+"/install.sh "+shQuote(o.Host)+" "+strconv.Itoa(o.VPNPort)), o.stdinFor(), logf)
	_, _ = sshRun(c, "rm -rf "+setupDir, nil, nil)
	if err != nil {
		return nil, err
	}
	if r.Code != 0 {
		return nil, errors.New("установка завершилась с ошибкой: " + errorText(r))
	}
	return &o, nil
}
