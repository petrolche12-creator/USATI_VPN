package main

// Общие функции, которые нужны и нативному окну Windows, и локальному интерфейсу на Linux.

import (
	"errors"
	"strings"
)

func (a *App) activeKeyID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil || (a.phase != "connected" && a.phase != "connecting") {
		return ""
	}
	return a.sess.KeyID
}

// buildOwnerForm проверяет поля формы сервера.
func buildOwnerForm(host, user string, sshPort int, pass string, vpnPort int, prev *Owner) (Owner, error) {
	host = strings.TrimSpace(host)
	if len(host) < 3 || len(host) > 253 {
		return Owner{}, errors.New("укажите IP или домен сервера")
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = "root"
	}
	if sshPort < 1 || sshPort > 65535 || vpnPort < 1 || vpnPort > 65535 {
		return Owner{}, errors.New("порты — числа от 1 до 65535")
	}
	if pass == "" {
		return Owner{}, errors.New("введите пароль SSH")
	}
	o := Owner{Host: host, SSHPort: sshPort, SSHUser: user, SSHPass: pass, VPNPort: vpnPort, IsRoot: user == "root"}
	if prev != nil && prev.Host == host && prev.SSHPort == sshPort {
		o.HostFP = prev.HostFP
	}
	return o, nil
}

func nameOK(s string) bool {
	s = strings.TrimSpace(s)
	return len([]rune(s)) >= 1 && len([]rune(s)) <= 40
}
