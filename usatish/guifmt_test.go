package main

// Проверки текстов интерфейса (общие для нативного окна Windows и мобильного клиента).

import (
	"strings"
	"testing"
	"time"
)

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:                 "0 Б",
		512:               "512 Б",
		1024:              "1 КБ",
		1536:              "1,5 КБ",
		20 * 1024 * 1024:  "20 МБ",
		13958643712:       "13 ГБ",
		53687091200:       "50 ГБ",
		1024 * 1024 * 512: "512 МБ",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, хочу %q", in, got, want)
		}
	}
}

func TestQuotaText(t *testing.T) {
	if got := quotaText(13958643712, 53687091200); got != "13 ГБ из 50 ГБ" {
		t.Errorf("quotaText с лимитом = %q", got)
	}
	if got := quotaText(1024, 0); got != "1 КБ, без лимита" {
		t.Errorf("quotaText без лимита = %q", got)
	}
}

func TestDateText(t *testing.T) {
	if got := dateText(0); got != "без срока" {
		t.Errorf("dateText(0) = %q", got)
	}
	ts := time.Date(2026, 10, 21, 12, 0, 0, 0, time.Local).Unix()
	if got := dateText(ts); got != "21.10.2026" {
		t.Errorf("dateText = %q", got)
	}
}

func TestDurationText(t *testing.T) {
	cases := map[int64]string{
		5:      "5 с",
		70:     "1 мин",
		3900:   "1 ч 05 мин",
		183600: "2 дн 3 ч",
		-3:     "0 с",
	}
	for in, want := range cases {
		if got := durationText(in); got != want {
			t.Errorf("durationText(%d) = %q, хочу %q", in, got, want)
		}
	}
}

func TestPhaseTitle(t *testing.T) {
	if got := phaseTitle("connected", "Подключено"); got != "Подключено" {
		t.Errorf("connected: %q", got)
	}
	if got := phaseTitle("connecting", ""); got != "Подключаюсь…" {
		t.Errorf("connecting: %q", got)
	}
	if got := phaseTitle("error", "таймаут"); got != "Ошибка подключения" {
		t.Errorf("error: %q", got)
	}
	if got := phaseTitle("idle", ""); got != "Не подключено" {
		t.Errorf("idle: %q", got)
	}
}

func TestStateTextsConnected(t *testing.T) {
	used, limit, exp := int64(1024*1024), int64(10*1024*1024*1024), int64(0)
	st := StateDTO{
		Phase: "connected", Message: "Подключено",
		Session: &SessionDTO{KeyName: "Дом", Host: "vpn.example", Transport: "MTP1",
			TransportLabel: "MTP/1 · UDP", RoutingLabel: "Весь трафик", ModeLabel: "Игровой"},
		StartedAt: time.Now().Add(-65 * time.Second).UnixMilli(),
		NowMs:     time.Now().UnixMilli(),
		Rx:        2048, Tx: 1024, Used: &used, Limit: &limit, Expires: &exp,
	}
	v := stateTexts(st, "Дом", shownChoice{})
	if v.Title != "Подключено" {
		t.Errorf("заголовок %q", v.Title)
	}
	if !strings.Contains(v.Key, "Дом") || !strings.Contains(v.Key, "vpn.example") {
		t.Errorf("строка ключа %q", v.Key)
	}
	if !strings.Contains(v.Details, "MTP/1 · UDP") || !strings.Contains(v.Details, "игровой режим") {
		t.Errorf("подробности %q", v.Details)
	}
	if !strings.Contains(v.Traffic, "1 мин") || !strings.Contains(v.Traffic, "2 КБ") {
		t.Errorf("трафик %q", v.Traffic)
	}
	if !strings.Contains(v.Quota, "1 МБ из 10 ГБ") || !strings.Contains(v.Quota, "без срока") {
		t.Errorf("лимит %q", v.Quota)
	}
}

func TestStateTextsIdleWithError(t *testing.T) {
	st := StateDTO{Phase: "idle", Message: "Не подключено"}
	v := stateTexts(st, "Ключ", shownChoice{Error: "для игрового режима нужен MTP/1"})
	if v.Details != "Для этого ключа выбранный режим недоступен" {
		t.Errorf("подробности %q", v.Details)
	}
	if v.Notice != "для игрового режима нужен MTP/1" {
		t.Errorf("предупреждение %q", v.Notice)
	}
	if v.Key != "Выбран ключ «Ключ»" {
		t.Errorf("строка ключа %q", v.Key)
	}
}

func TestKeyLine(t *testing.T) {
	sk := SavedKey{ID: "a", Alias: "Дом", Transport: TMTP1, Routing: RAll,
		Stat: &KeyStat{Used: 1024, Limit: 0, Expires: 0, At: 1}}
	line := keyLine(sk, "vpn.example", true, false, "idle")
	if !strings.HasPrefix(line, "›") || !strings.Contains(line, "Дом") ||
		!strings.Contains(line, "vpn.example") || !strings.Contains(line, "MTP/1 · UDP") ||
		!strings.Contains(line, "Весь трафик") || !strings.Contains(line, "1 КБ, без лимита") {
		t.Errorf("строка ключа %q", line)
	}
	active := keyLine(sk, "h", true, true, "connected")
	if !strings.HasPrefix(active, "●") {
		t.Errorf("активный ключ %q", active)
	}
}

func TestServerInfoText(t *testing.T) {
	title, details := serverInfoText(ServerInfo{}, false)
	if title != "Сервер не настроен" || details == "" {
		t.Errorf("ненастроенный сервер: %q / %q", title, details)
	}
	info := ServerInfo{Configured: true, Host: "1.2.3.4", SSHUser: "root", SSHPort: 22, VPNPort: 443, Users: 3, Online: 1}
	title, details = serverInfoText(info, true)
	if !strings.Contains(title, "1.2.3.4") || !strings.Contains(details, "ключей 3") || !strings.Contains(details, "в сети 1") {
		t.Errorf("настроенный сервер: %q / %q", title, details)
	}
}

func TestServerUserLine(t *testing.T) {
	u := adminUser{Name: "Маша", IP: "10.9.0.3", Status: "активен", Used: 0, Limit: 0, Online: true}
	line := serverUserLine(u)
	if !strings.Contains(line, "Маша") || !strings.Contains(line, "активен") || !strings.Contains(line, "без срока") {
		t.Errorf("строка ключа сервера %q", line)
	}
	if !strings.HasPrefix(line, "●") {
		t.Errorf("онлайн-отметка %q", line)
	}
}

func TestKeyStatText(t *testing.T) {
	if got := keyStatText(nil); !strings.Contains(got, "пока нет") {
		t.Errorf("пустые сведения %q", got)
	}
	st := &KeyStat{Used: 2048, Limit: 4096, Expires: 0, At: time.Now().UnixMilli()}
	got := keyStatText(st)
	if !strings.Contains(got, "2 КБ из 4 КБ") || !strings.Contains(got, "без срока") {
		t.Errorf("сведения %q", got)
	}
}

func TestChainText(t *testing.T) {
	empty := stateTexts(StateDTO{Phase: "idle"}, "", shownChoice{})
	if !strings.Contains(empty.Chain, "загрузите") {
		t.Fatalf("для пустого состояния должна быть подсказка, а не %q", empty.Chain)
	}
	full := stateTexts(StateDTO{Phase: "idle", Chain: ChainDTO{Amnezia: "amnezia", HasSSH: true}}, "", shownChoice{})
	if !strings.Contains(full.Chain, "Готово к подключению") {
		t.Fatalf("всё готово, а текст %q", full.Chain)
	}
	half := stateTexts(StateDTO{Phase: "idle", Chain: ChainDTO{Amnezia: "amnezia"}}, "", shownChoice{})
	if !strings.Contains(half.Chain, "Уже есть") || !strings.Contains(half.Chain, "SSH") {
		t.Fatalf("загружен только конфиг, а текст %q", half.Chain)
	}
	direct := stateTexts(StateDTO{Phase: "idle", Chain: ChainDTO{Direct: true}}, "", shownChoice{})
	if !strings.Contains(direct.Chain, "Аварийный режим") {
		t.Fatalf("прямой режим: %q", direct.Chain)
	}
	stage := stateTexts(StateDTO{Phase: "connecting", Chain: ChainDTO{Stage: "awg"}}, "", shownChoice{})
	if !strings.Contains(stage.Chain, "AmneziaWG") {
		t.Fatalf("этап туннеля: %q", stage.Chain)
	}
}
