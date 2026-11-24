package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// modesKey — ключ со всеми способами. Перечисленные способы в нём убираются.
func modesKey(drop ...Transport) (*ServerKey, SavedKey) {
	k := &ServerKey{
		Name: "Вася", Host: "203.0.113.5", UID: bytes.Repeat([]byte{1}, 8), Secret: bytes.Repeat([]byte{2}, 32),
		Ports: map[string]int{"udp": 443, "tcp": 8443, "mask": 443, "wg": 51820, "ss": 20001},
		Wg: &WgParams{
			PrivateKey:      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
			ServerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
			Address:         "10.9.0.2/32", DNS: "1.1.1.1",
		},
		Ss: &SsParams{Method: "chacha20-ietf-poly1305", Password: "пароль-123"},
	}
	for _, t := range drop {
		switch t {
		case TMTP1:
			delete(k.Ports, "udp")
		case TMTP2:
			delete(k.Ports, "tcp")
		case TMTP3:
			delete(k.Ports, "mask")
		case TWG:
			k.Wg = nil
		case TSS:
			k.Ss = nil
		}
	}
	// У ключа в списке свой способ и маршрутизация: TCP-вариант и «Кроме российских IP».
	return k, SavedKey{ID: "k1", Text: "mtp://test", Transport: TMTP2, Routing: RNotRU}
}

// TestModesDefaultKeepsKey: по умолчанию работает то же, что было до режимов.
func TestModesDefaultKeepsKey(t *testing.T) {
	key, sk := modesKey()
	ch, err := EffectiveChoice(DefaultSettings(), sk, key)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Transport != TMTP2 || ch.Routing != RNotRU || ch.Mode != ModeNormal {
		t.Fatalf("по умолчанию должно быть как в ключе, получили %+v", ch)
	}
}

// TestModeWorksWithoutManualProtocol: режим меняет работу сразу, без всяких переключателей.
func TestModeWorksWithoutManualProtocol(t *testing.T) {
	key, sk := modesKey()
	ch, err := EffectiveChoice(AppSettings{Advanced: false, Mode: ModeGame, Protocol: ProtoAuto}, sk, key)
	if err != nil || ch.Transport != TMTP1 || ch.Routing != RNotRU || ch.Mode != ModeGame {
		t.Fatalf("игровой режим без ручного протокола: UDP, маршрутизация ключа, получили %+v, %v", ch, err)
	}
	ch, err = EffectiveChoice(AppSettings{Advanced: false, Mode: ModeAnon, Protocol: ProtoAuto}, sk, key)
	if err != nil || ch.Transport != TMTP3 || ch.Routing != RAll {
		t.Fatalf("анонимный режим без ручного протокола: MTP/3 и весь трафик, получили %+v, %v", ch, err)
	}
}

// TestModesGameUsesUDP: игровой режим берёт UDP: сначала MTP/1, без него WireGuard.
func TestModesGameUsesUDP(t *testing.T) {
	s := AppSettings{Advanced: false, Mode: ModeGame, Protocol: ProtoAuto}
	key, sk := modesKey()
	ch, err := EffectiveChoice(s, sk, key)
	if err != nil || ch.Transport != TMTP1 || ch.Routing != RNotRU {
		t.Fatalf("игровой режим должен взять MTP/1 и маршрутизацию ключа: %+v, %v", ch, err)
	}
	key, sk = modesKey(TMTP1)
	ch, err = EffectiveChoice(s, sk, key)
	if err != nil || ch.Transport != TWG {
		t.Fatalf("без MTP/1 игровой режим должен взять WireGuard: %+v, %v", ch, err)
	}
	key, sk = modesKey(TMTP1, TWG)
	if _, err := EffectiveChoice(s, sk, key); err == nil {
		t.Fatal("игровой режим без UDP-способов должен давать ошибку")
	}
}

// TestModesAnonForcesMaskAndAllTraffic: анонимный режим берёт MTP/3 и весь трафик через VPN.
func TestModesAnonForcesMaskAndAllTraffic(t *testing.T) {
	s := AppSettings{Advanced: false, Mode: ModeAnon, Protocol: ProtoAuto}
	key, sk := modesKey()
	ch, err := EffectiveChoice(s, sk, key)
	if err != nil || ch.Transport != TMTP3 || ch.Routing != RAll {
		t.Fatalf("анонимный режим: MTP/3 и весь трафик через VPN, получили %+v, %v", ch, err)
	}
	key, sk = modesKey(TMTP3)
	if _, err := EffectiveChoice(s, sk, key); err == nil {
		t.Fatal("анонимный режим без MTP/3 должен давать ошибку, а не тихо падать на другой способ")
	}
}

// TestProtocolNeedsManualSwitch: протокол, выбранный вручную, работает только при включённом ручном выборе.
func TestProtocolNeedsManualSwitch(t *testing.T) {
	key, sk := modesKey()
	off := AppSettings{Advanced: false, Mode: ModeNormal, Protocol: string(TWG)}
	ch, err := EffectiveChoice(off, sk, key)
	if err != nil || ch.Transport != TMTP2 {
		t.Fatalf("без ручного выбора протокол не меняется, получили %+v, %v", ch, err)
	}
	on := AppSettings{Advanced: true, Mode: ModeNormal, Protocol: string(TWG)}
	ch, err = EffectiveChoice(on, sk, key)
	if err != nil || ch.Transport != TWG || ch.Routing != RNotRU {
		t.Fatalf("ручной выбор WireGuard: %+v, %v", ch, err)
	}
	key, sk = modesKey(TWG)
	if _, err := EffectiveChoice(on, sk, key); err == nil {
		t.Fatal("протокол, которого нет в ключе, должен давать ошибку")
	}
}

// TestModesExplicitProtocolInAnon: в анонимном режиме выбранный вручную протокол работает, а маршрутизация остаётся весь трафик.
func TestModesExplicitProtocolInAnon(t *testing.T) {
	s := AppSettings{Advanced: true, Mode: ModeAnon, Protocol: string(TMTP1)}
	key, sk := modesKey()
	ch, err := EffectiveChoice(s, sk, key)
	if err != nil || ch.Transport != TMTP1 || ch.Routing != RAll {
		t.Fatalf("анонимный режим с ручным MTP/1: %+v, %v", ch, err)
	}
}

// TestSettingsStore: настройки сохраняются, по умолчанию выключены, неверные значения не пишутся.
func TestSettingsStore(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadSettings(dir)
	if err != nil || s != DefaultSettings() {
		t.Fatalf("без файла должны быть настройки по умолчанию: %+v, %v", s, err)
	}
	if s.Advanced || s.Mode != ModeNormal || s.Protocol != ProtoAuto {
		t.Fatalf("по умолчанию ручной протокол выключен, режим обычный: %+v", s)
	}
	want := AppSettings{Advanced: true, Mode: ModeAnon, Protocol: string(TMTP3)}
	if err := SaveSettings(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(dir)
	if err != nil || got != want {
		t.Fatalf("настройки не сохранились: %+v, %v", got, err)
	}
	if err := SaveSettings(dir, AppSettings{Mode: "turbo", Protocol: ProtoAuto}); err == nil {
		t.Fatal("неизвестный режим не должен сохраняться")
	}
	if err := SaveSettings(dir, AppSettings{Mode: ModeNormal, Protocol: string(TSS)}); err == nil {
		t.Fatal("Shadowsocks не выбирается в настройках протокола")
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSettings(dir); err == nil || got != DefaultSettings() {
		t.Fatalf("испорченный файл: должны быть настройки по умолчанию и предупреждение, получили %+v, %v", got, err)
	}
}

// TestModesRouting: игровой режим уводит российские сети мимо VPN (пинг как без VPN),
// анонимный — собирает весь трафик в туннель, даже если в ключе «весь трафик» или наоборот.
func TestModesRouting(t *testing.T) {
	key, skAll := modesKey()
	skAll.Routing = RAll
	game, err := EffectiveChoice(AppSettings{Mode: ModeGame}, skAll, key)
	if err != nil {
		t.Fatalf("game: %v", err)
	}
	if game.Routing != RNotRU || game.Transport != TMTP1 {
		t.Fatalf("игровой режим: хочу MTP/1 и «кроме российских IP», got %s+%s", game.Transport, game.Routing)
	}
	anon, err := EffectiveChoice(AppSettings{Mode: ModeAnon}, skAll, key)
	if err != nil {
		t.Fatalf("anon: %v", err)
	}
	if anon.Routing != RAll || anon.Transport != TMTP3 {
		t.Fatalf("анонимный режим: хочу MTP/3 и «весь трафик», got %s+%s", anon.Transport, anon.Routing)
	}
	man, err := EffectiveChoice(AppSettings{Mode: ModeGame, Advanced: true, Protocol: string(TMTP1)}, skAll, key)
	if err != nil {
		t.Fatalf("manual: %v", err)
	}
	if man.Routing != RNotRU {
		t.Fatalf("ручной протокол в игровом режиме потерял маршрутизацию: %s", man.Routing)
	}
}
