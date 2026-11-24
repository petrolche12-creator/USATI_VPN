//go:build !windows

package main

// Локальный HTTP-интерфейс — только для проверок разработчиком на Linux (main_other.go).
// В Windows-версии этот файл не собирается: там нативное окно Win32, и программа не слушает порты.

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// Handler собирает маршруты. token и port защищают API от других страниц в браузере.
func (a *App) Handler(token string, port int) http.Handler {
	mux := http.NewServeMux()
	uiRoot, _ := fs.Sub(uiFS, "ui")
	files := http.FileServer(http.FS(uiRoot))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) != 1 {
				http.Error(w, "доступ только из окна программы", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			b, err := fs.ReadFile(uiRoot, "index.html")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		files.ServeHTTP(w, r)
	})

	api := func(method string, h func(w http.ResponseWriter, r *http.Request) (any, error)) {
		mux.HandleFunc("/api/"+method, func(w http.ResponseWriter, r *http.Request) {
			if !a.localRequest(w, r, token, port) {
				return
			}
			out, err := h(w, r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, out)
		})
	}

	api("state", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return a.State(), nil
	})
	api("ping", func(w http.ResponseWriter, r *http.Request) (any, error) {
		a.Ping()
		return map[string]bool{"ok": true}, nil
	})
	api("quit", func(w http.ResponseWriter, r *http.Request) (any, error) {
		a.Disconnect()
		a.Quit()
		return map[string]bool{"ok": true}, nil
	})
	api("settings", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return a.settingsReply(), nil
	})
	api("settings/save", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in AppSettings
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		return a.settingsSave(in)
	})
	api("keys", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return a.keysReply(), nil
	})
	api("keys/add", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		sk, created, replaced, err := a.keys.AddOrUpdate(in.Text)
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": sk.ID, "created": created, "replaced": replaced, "name": sk.DisplayName()}, nil
	})
	api("keys/check", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Text string `json:"text"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		key, err := ParseKey(in.Text)
		if err != nil {
			return nil, err
		}
		existing, same := "", false
		for _, sk := range a.keys.List() {
			if sk.ID == key.UIDHex() {
				existing = sk.DisplayName()
				same = sk.Text == strings.TrimSpace(in.Text)
			}
		}
		supports := map[string]bool{}
		for _, t := range allTransports {
			supports[string(t)] = key.Supports(t)
		}
		return map[string]any{"name": key.Name, "host": key.Host, "supports": supports,
			"existing": existing, "same": same}, nil
	})
	api("keys/update", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			ID        string   `json:"id"`
			Alias     string   `json:"alias"`
			Transport string   `json:"transport"`
			Routing   string   `json:"routing"`
			Apps      []string `json:"apps"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		sk, ok := a.keys.Find(in.ID)
		if !ok {
			return nil, errors.New("ключ не найден")
		}
		t := Transport(in.Transport)
		rt := Routing(in.Routing)
		if !t.Valid() || !rt.Valid() {
			return nil, errors.New("неверный способ или маршрутизация")
		}
		sk.Alias = strings.TrimSpace(in.Alias)
		sk.Transport = t
		sk.Routing = rt
		sk.Apps = in.Apps
		if err := a.keys.Update(sk); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	})
	api("keys/delete", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		if a.activeKeyID() == in.ID {
			return nil, errors.New("сначала отключите этот ключ")
		}
		return map[string]bool{"ok": true}, a.keys.Remove(in.ID)
	})
	api("keys/select", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.keys.Select(in.ID)
	})
	api("connect", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		if err := a.keys.Select(in.ID); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.Connect(in.ID)
	})
	api("disconnect", func(w http.ResponseWriter, r *http.Request) (any, error) {
		a.Disconnect()
		return map[string]bool{"ok": true}, nil
	})
	api("copy-ss", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		sk, ok := a.keys.Find(in.ID)
		if !ok {
			return nil, errors.New("ключ не найден")
		}
		key, err := ParseKey(sk.Text)
		if err != nil {
			return nil, err
		}
		link, ok := SSLink(key)
		if !ok {
			return nil, errors.New("в ключе нет параметров Shadowsocks")
		}
		return map[string]string{"link": link}, nil
	})

	api("server", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return a.ServerStatus(), nil
	})
	api("server/setup", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Host    string `json:"host"`
			SSHUser string `json:"sshUser"`
			SSHPort int    `json:"sshPort"`
			SSHPass string `json:"sshPass"`
			VPNPort int    `json:"vpnPort"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		form, err := buildOwnerForm(in.Host, in.SSHUser, in.SSHPort, in.SSHPass, in.VPNPort, a.Owner())
		if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.InstallServer(form)
	})
	api("job", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return a.Job(), nil
	})
	api("server/keys", func(w http.ResponseWriter, r *http.Request) (any, error) {
		l, err := a.ServerKeys()
		if err != nil {
			return nil, err
		}
		return l, nil
	})
	api("server/key/add", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Name  string  `json:"name"`
			Days  int     `json:"days"`
			Limit float64 `json:"limit"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		o := a.Owner()
		if o == nil {
			return nil, errors.New("сервер не настроен")
		}
		if !nameOK(in.Name) {
			return nil, errors.New("имя ключа: от 1 до 40 символов")
		}
		if in.Days < 0 || in.Days > 3650 {
			return nil, errors.New("срок: от 0 до 3650 дней, 0 — без срока")
		}
		if in.Limit < 0 || in.Limit > 100000 {
			return nil, errors.New("лимит: число ГБ, 0 — без лимита")
		}
		key, err := adminAddKey(o, in.Name, in.Days, in.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]string{"key": key}, nil
	})
	api("server/key/show", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		o := a.Owner()
		if o == nil {
			return nil, errors.New("сервер не настроен")
		}
		key, err := adminShowKey(o, in.Name)
		if err != nil {
			return nil, err
		}
		return map[string]string{"key": key}, nil
	})
	api("server/key/action", func(w http.ResponseWriter, r *http.Request) (any, error) {
		var in struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		if err := readBody(r, &in); err != nil {
			return nil, err
		}
		o := a.Owner()
		if o == nil {
			return nil, errors.New("сервер не настроен")
		}
		var err error
		switch in.Action {
		case "enable":
			err = adminSetEnabled(o, in.Name, true)
		case "disable":
			err = adminSetEnabled(o, in.Name, false)
		case "reset":
			err = adminResetTraffic(o, in.Name)
		case "delete":
			err = adminDelete(o, in.Name)
		default:
			err = errors.New("неизвестное действие")
		}
		return map[string]bool{"ok": err == nil}, err
	})
	api("server/forget", func(w http.ResponseWriter, r *http.Request) (any, error) {
		return map[string]bool{"ok": true}, a.ForgetOwner()
	})

	return mux
}

// localRequest проверяет, что запрос пришёл с этого компьютера и с правильным токеном.
func (a *App) localRequest(w http.ResponseWriter, r *http.Request, token string, port int) bool {
	if r.Host != fmt.Sprintf("127.0.0.1:%d", port) && r.Host != fmt.Sprintf("localhost:%d", port) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// keysReply — список ключей для интерфейса.
func (a *App) keysReply() map[string]any {
	active := a.activeKeyID()
	list := a.keys.List()
	out := make([]map[string]any, 0, len(list))
	for _, sk := range list {
		item := map[string]any{
			"id": sk.ID, "name": sk.DisplayName(), "alias": sk.Alias,
			"transport": string(sk.Transport), "transportLabel": sk.Transport.Label(),
			"routing": string(sk.Routing), "routingLabel": sk.Routing.Label(),
			"apps": sk.Apps, "text": sk.Text, "active": sk.ID == active,
			"selected": sk.ID == a.keys.Selected(), "stat": sk.Stat,
		}
		supports := map[string]bool{}
		host := ""
		if key, err := ParseKey(sk.Text); err == nil {
			host = key.Host
			for _, t := range allTransports {
				supports[string(t)] = key.Supports(t)
			}
		}
		item["host"] = host
		item["supports"] = supports
		out = append(out, item)
	}
	transports := make([]map[string]any, 0, len(allTransports))
	for _, t := range allTransports {
		transports = append(transports, map[string]any{"id": string(t), "label": t.Label(), "hint": t.Hint(), "own": t.Own()})
	}
	routings := make([]map[string]any, 0, 3)
	for _, r := range []Routing{RAll, RApps, RNotRU} {
		routings = append(routings, map[string]any{"id": string(r), "label": r.Label(), "hint": r.Hint()})
	}
	return map[string]any{"keys": out, "selected": a.keys.Selected(), "transports": transports, "routings": routings, "effective": a.effectiveFor(a.keys.Selected())}
}

func readBody(r *http.Request, dst any) error {
	if r.Method != http.MethodPost {
		return errors.New("нужен POST")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return errors.New("неверные данные запроса")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// timeNowMs — текущее время в миллисекундах (для интерфейса).
func timeNowMs() int64 { return time.Now().UnixMilli() }
