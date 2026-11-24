package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Mode — режим работы. Выбирается всегда, не зависит от ручного выбора протокола.
//   - game: минимальный пинг и максимальная скорость: UDP без TCP-обёртки (MTP/1, иначе WireGuard);
//   - normal: баланс: протокол и маршрутизация берутся из ключа, как раньше;
//   - anon: максимальная скрытность: MTP/3 (маскировка под TLS) и весь трафик через VPN.
type Mode string

const (
	ModeGame   Mode = "game"
	ModeNormal Mode = "normal"
	ModeAnon   Mode = "anon"
)

// ProtoAuto — протокол выбирает режим.
const ProtoAuto = "auto"

type modeInfo struct {
	label string
	hint  string
}

var modeTable = map[Mode]modeInfo{
	ModeGame: {
		label: "Игровой",
		hint:  "минимальный пинг: UDP без маскировки (MTP/1, иначе WireGuard), трафик к российским сайтам идёт напрямую, мимо VPN, и их домены резолвятся у провайдера без петли через туннель — пинг до них как без VPN",
	},
	ModeNormal: {
		label: "Обычный",
		hint:  "баланс скорости и защиты: протокол и маршрутизация берутся из ключа, как раньше",
	},
	ModeAnon: {
		label: "Максимально анонимный",
		hint:  "маскировка под HTTPS (MTP/3), весь трафик и DNS только через VPN, IPv6 на время отключён, часовые пояса компьютера временно ставятся как на сервере (возвращаются при отключении). Это не абсолютная анонимность: сервер видит трафик",
	},
}

var modeOrder = []Mode{ModeGame, ModeNormal, ModeAnon}

// AppSettings — настройки программы (файл settings.json в папке данных).
//   - Mode: режим, действует всегда;
//   - Advanced: включён ли ручной выбор протокола. Выключено — протокол выбирает режим;
//   - Protocol: протокол, выбранный вручную (учитывается, только когда Advanced включено).
type AppSettings struct {
	Advanced bool   `json:"advanced"`
	Mode     Mode   `json:"mode"`
	Protocol string `json:"protocol"`
	// AmneziaAsVpn — отдать весь трафик компьютера в туннель AmneziaWG (а MTP-VPN поверх SSH
	// работает внутри него). Выключено — в AmneziaWG идёт только SSH, остальное напрямую через MTP.
	AmneziaAsVpn bool `json:"amneziaAsVpn"`
	// MTPDirect — аварийный режим: MTP без Amnezia и без SSH, напрямую на порт VPN.
	// Нужен, только если хостинг режет всё, кроме Amnezia, или сервер поставлен старой программой.
	MTPDirect bool `json:"mtpDirect"`
}

// DefaultSettings — настройки по умолчанию: обычный режим, протокол выбирает режим.
func DefaultSettings() AppSettings {
	return AppSettings{Advanced: false, Mode: ModeNormal, Protocol: ProtoAuto, AmneziaAsVpn: false, MTPDirect: false}
}

// Valid проверяет значения: неизвестные режим и протокол не сохраняются.
func (s AppSettings) Valid() error {
	if _, ok := modeTable[s.Mode]; !ok {
		return errors.New("неизвестный режим")
	}
	switch Transport(s.Protocol) {
	case TMTP1, TMTP2, TMTP3, TWG:
		return nil
	}
	if s.Protocol == ProtoAuto {
		return nil
	}
	return errors.New("неизвестный протокол")
}

func settingsPath(dir string) string { return filepath.Join(dir, "settings.json") }

// LoadSettings читает настройки. Если файла нет, возвращаются настройки по умолчанию.
// Если файл испорчен, тоже возвращаются настройки по умолчанию, и вызывающий получает предупреждение.
func LoadSettings(dir string) (AppSettings, error) {
	b, err := os.ReadFile(settingsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultSettings(), nil
		}
		return DefaultSettings(), err
	}
	s := DefaultSettings()
	if err := json.Unmarshal(b, &s); err != nil || s.Valid() != nil {
		return DefaultSettings(), errors.New("файл настроек повреждён, используются настройки по умолчанию")
	}
	return s, nil
}

// SaveSettings пишет настройки атомарно (через временный файл).
func SaveSettings(dir string, s AppSettings) error {
	if err := s.Valid(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := settingsPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath(dir))
}

// Choice — способ и маршрутизация, которые пойдут в туннель при подключении ключа.
type Choice struct {
	Transport Transport
	Routing   Routing
	Mode      Mode
}

// EffectiveChoice решает, с каким способом и маршрутизацией подключать ключ.
// Сам ключ не меняется: выбор действует только на это подключение.
// Если нужного способа в ключе нет, возвращается ошибка, а протокол тихо не подменяется.
func EffectiveChoice(s AppSettings, sk SavedKey, key *ServerKey) (Choice, error) {
	mode := s.Mode
	proto := ProtoAuto
	if s.Advanced {
		proto = s.Protocol
	}
	c := Choice{Transport: sk.Transport, Routing: sk.Routing, Mode: mode}
	if mode == ModeAnon {
		c.Routing = RAll // анонимный режим: весь трафик через VPN, без прямых соединений
	}
	if mode == ModeGame {
		c.Routing = RNotRU // игровой режим: российские сети напрямую, пинг до них как без VPN
	}
	if proto != ProtoAuto {
		t := Transport(proto)
		if !key.Supports(t) {
			return c, fmt.Errorf("в ключе нет параметров для «%s». Выберите другой протокол или другой ключ", t.Label())
		}
		c.Transport = t
		return c, nil
	}
	switch mode {
	case ModeGame:
		for _, t := range []Transport{TMTP1, TWG} {
			if key.Supports(t) {
				c.Transport = t
				return c, nil
			}
		}
		return c, errors.New("для игрового режима нужен MTP/1 или WireGuard, а в ключе их нет. Выберите обычный режим или другой ключ")
	case ModeAnon:
		if key.Supports(TMTP3) {
			c.Transport = TMTP3
			return c, nil
		}
		return c, errors.New("для максимально анонимного режима нужен MTP/3 (маскировка), а в ключе его нет. Выберите обычный режим или другой ключ")
	}
	return c, nil
}

// effectiveFor — что будет использовано для выбранного ключа при текущих настройках (для экрана, без подключения).
func (a *App) effectiveFor(id string) map[string]any {
	sk, ok := a.keys.Find(id)
	if !ok {
		return nil
	}
	a.mu.Lock()
	set := a.settings
	a.mu.Unlock()
	key, err := ParseKey(sk.Text)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	ch, err := EffectiveChoice(set, sk, key)
	out := map[string]any{
		"transport": string(ch.Transport), "transportLabel": ch.Transport.Label(),
		"routing": string(ch.Routing), "routingLabel": ch.Routing.Label(),
		"mode": string(ch.Mode), "modeLabel": modeLabel(ch.Mode),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

// settingsReply — настройки и варианты выбора для экрана «Настройки».
func (a *App) settingsReply() map[string]any {
	a.mu.Lock()
	s := a.settings
	a.mu.Unlock()
	modes := make([]map[string]any, 0, len(modeOrder))
	for _, m := range modeOrder {
		info := modeTable[m]
		modes = append(modes, map[string]any{"id": string(m), "label": info.label, "hint": info.hint})
	}
	protos := []map[string]any{{"id": ProtoAuto, "label": "Авто (по режиму)", "hint": "игровой: UDP, анонимный: MTP/3, обычный: как в ключе"}}
	for _, t := range []Transport{TMTP1, TMTP2, TMTP3, TWG} {
		protos = append(protos, map[string]any{"id": string(t), "label": t.Label(), "hint": t.Hint()})
	}
	return map[string]any{
		"advanced": s.Advanced, "mode": string(s.Mode), "protocol": s.Protocol,
		"amneziaAsVpn": s.AmneziaAsVpn, "mtpDirect": s.MTPDirect,
		"modes": modes, "protocols": protos,
	}
}

// settingsSave проверяет и сохраняет настройки из интерфейса; действуют при следующем подключении.
func (a *App) settingsSave(in AppSettings) (map[string]any, error) {
	if err := SaveSettings(a.dir, in); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.settings = in
	a.mu.Unlock()
	return a.settingsReply(), nil
}

func modeLabel(m Mode) string {
	return modeTable[m].label
}
