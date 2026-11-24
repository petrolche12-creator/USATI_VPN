package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ownerFile — сохранённый сервер. Пароль SSH хранится в зашифрованном виде (DPAPI на Windows).
type ownerFile struct {
	Host    string `json:"host"`
	SSHPort int    `json:"sshPort"`
	SSHUser string `json:"sshUser"`
	VPNPort int    `json:"vpnPort"`
	IsRoot  bool   `json:"isRoot"`
	HostFP  string `json:"hostFp"`
	PassEnc string `json:"passEnc"`
}

func ownerPath(dir string) string { return filepath.Join(dir, "server.json") }

// LoadOwner читает сохранённый сервер; nil, если его нет.
func LoadOwner(dir string) (*Owner, error) {
	b, err := os.ReadFile(ownerPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f ownerFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	enc, err := base64.StdEncoding.DecodeString(f.PassEnc)
	if err != nil {
		return nil, err
	}
	pass, err := unprotectSecret(enc)
	if err != nil {
		return nil, err
	}
	return &Owner{
		Host: f.Host, SSHPort: f.SSHPort, SSHUser: f.SSHUser, SSHPass: string(pass),
		VPNPort: f.VPNPort, IsRoot: f.IsRoot, HostFP: f.HostFP,
	}, nil
}

// SaveOwner сохраняет сервер на компьютере.
func SaveOwner(dir string, o *Owner) error {
	enc, err := protectSecret([]byte(o.SSHPass))
	if err != nil {
		return err
	}
	f := ownerFile{
		Host: o.Host, SSHPort: o.SSHPort, SSHUser: o.SSHUser, VPNPort: o.VPNPort,
		IsRoot: o.IsRoot, HostFP: o.HostFP, PassEnc: base64.StdEncoding.EncodeToString(enc),
	}
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := ownerPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ownerPath(dir))
}

// ForgetOwner стирает сохранённый сервер с компьютера.
func ForgetOwner(dir string) error {
	err := os.Remove(ownerPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
