package main

// Хранение загруженного .conf Amnezia на компьютере: %APPDATA%\UsatyiVPN\amnezia.conf.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

var awgActive atomic.Pointer[AwgConfig]

// activeAwg — загруженный конфиг Amnezia (nil, если файла нет).
func activeAwg() *AwgConfig { return awgActive.Load() }

func amneziaPath(dir string) string { return filepath.Join(dir, "amnezia.conf") }

// LoadAmnezia читает сохранённый конфиг, если он есть.
func LoadAmnezia(dir string) (*AwgConfig, error) {
	b, err := os.ReadFile(amneziaPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c, err := ParseAwgConf(string(b))
	if err != nil {
		return nil, err
	}
	c.Path = "amnezia.conf"
	awgActive.Store(c)
	return c, nil
}

// SaveAmnezia сохраняет конфиг из файла, который выбрал пользователь, и проверяет его.
func SaveAmnezia(dir, srcPath string) (*AwgConfig, error) {
	b, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, err
	}
	c, err := ParseAwgConf(string(b))
	if err != nil {
		return nil, err
	}
	c.Path = srcPath
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp := amneziaPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, amneziaPath(dir)); err != nil {
		return nil, err
	}
	awgActive.Store(c)
	return c, nil
}

// ForgetAmnezia удаляет сохранённый конфиг.
func ForgetAmnezia(dir string) error {
	awgActive.Store(nil)
	err := os.Remove(amneziaPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

var amneziaMu sync.Mutex

// amneziaSummary — короткое описание конфига для окна.
func amneziaSummary(c *AwgConfig) []string {
	if c == nil {
		return []string{"Файл .conf Amnezia не загружен."}
	}
	lines := []string{
		"Файл: " + c.DisplayName(),
		"Протокол: " + c.Kind(),
		"Сервер: " + c.Peer.Endpoint,
		"Адрес в туннеле: " + strings.Join(c.Iface.Address, ", "),
	}
	if c.Kind() == "AmneziaWG" {
		obf := []string{}
		add := func(name, v string) {
			if strings.TrimSpace(v) != "" {
				obf = append(obf, name+"="+v)
			}
		}
		add("Jc", c.Iface.Jc)
		add("Jmin", c.Iface.Jmin)
		add("Jmax", c.Iface.Jmax)
		add("S1", c.Iface.S1)
		add("S2", c.Iface.S2)
		add("H1", c.Iface.H1)
		add("H2", c.Iface.H2)
		if len(obf) > 0 {
			lines = append(lines, "Обфускация: "+strings.Join(obf, ", "))
		}
	}
	if mt := c.Iface.MTU; mt > 0 {
		lines = append(lines, "MTU: "+itoa(mt))
	}
	return lines
}
