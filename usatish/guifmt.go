package main

// Тексты интерфейса. Эти функции не зависят от Win32 и от браузера: их использует
// нативное окно Windows (gui_windows.go), и их же можно проверять обычными тестами на Linux.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// numText — число с запятой как разделителем и без лишних нулей: 12,3 / 50 / 1,25.
func numText(v float64, digits int) string {
	s := strconv.FormatFloat(v, 'f', digits, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		s = "0"
	}
	return strings.Replace(s, ".", ",", 1)
}

// humanBytes — объём в привычных единицах: 512 Б, 1,5 КБ, 20,4 МБ, 12,3 ГБ.
func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "0 Б"
	case n < 1024:
		return strconv.FormatInt(n, 10) + " Б"
	case n < 1024*1024:
		return numText(float64(n)/1024, 1) + " КБ"
	case n < 1024*1024*1024:
		return numText(float64(n)/(1024*1024), 1) + " МБ"
	default:
		return numText(float64(n)/(1024*1024*1024), 2) + " ГБ"
	}
}

// quotaText — «12,3 ГБ из 50 ГБ» или «12,3 ГБ, без лимита».
func quotaText(used, limit int64) string {
	if limit > 0 {
		return humanBytes(used) + " из " + humanBytes(limit)
	}
	return humanBytes(used) + ", без лимита"
}

// dateText — дата по метке в секундах; «без срока», если срок не задан.
func dateText(unixSec int64) string {
	if unixSec <= 0 {
		return "без срока"
	}
	return time.Unix(unixSec, 0).Format("02.01.2006")
}

// stampText — когда получены данные: «09.10 18:05».
func stampText(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("02.01 15:04")
}

// durationText — длительность сеанса: «45 с», «7 мин», «1 ч 05 мин», «2 дн 3 ч».
func durationText(sec int64) string {
	if sec < 0 {
		sec = 0
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	switch {
	case d > 0:
		return fmt.Sprintf("%d дн %d ч", d, h)
	case h > 0:
		return fmt.Sprintf("%d ч %02d мин", h, m)
	case m > 0:
		return fmt.Sprintf("%d мин", m)
	default:
		return fmt.Sprintf("%d с", s)
	}
}

// phaseTitle — крупный заголовок состояния окна.
func phaseTitle(phase, message string) string {
	switch phase {
	case "connecting":
		return "Подключаюсь…"
	case "connected":
		return "Подключено"
	case "error":
		if message != "" {
			return "Ошибка подключения"
		}
		return "Ошибка"
	default:
		return "Не подключено"
	}
}

// shownChoice — способ, маршрутизация и режим, которые показывает окно.
type shownChoice struct {
	TransportLabel string
	RoutingLabel   string
	ModeLabel      string
	Error          string
}

// choiceOfEffective преобразует ответ effectiveFor (map из настроек) в shownChoice.
func choiceOfEffective(m map[string]any) shownChoice {
	c := shownChoice{}
	if m == nil {
		return c
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	c.TransportLabel, c.RoutingLabel, c.ModeLabel, c.Error = str("transportLabel"), str("routingLabel"), str("modeLabel"), str("error")
	return c
}

// viewTexts — все строки главного экрана.
type viewTexts struct {
	Title   string
	Key     string
	Details string
	Notice  string
	Traffic string
	Quota   string
	Chain   string
}

// chainText — что уже готово для цепочки «.conf Amnezia → SSH → MTP» и чего не хватает.
// Показывается на странице «Подключение», чтобы было видно каждый шаг.
func chainText(st StateDTO) string {
	c := st.Chain
	var have, need []string
	if c.Amnezia != "" {
		have = append(have, "AmneziaWG: «"+c.Amnezia+"»")
	} else {
		need = append(need, "файл .conf Amnezia")
	}
	if c.HasSSH {
		have = append(have, "SSH к серверу")
	} else {
		need = append(need, "данные SSH (IP, пользователь, пароль)")
	}
	if c.Direct {
		return "Аварийный режим: подключение к MTP-VPN напрямую, без Amnezia и SSH."
	}
	switch {
	case c.Stage == "awg":
		return "Шаг 1 из 3: поднимаю туннель AmneziaWG…"
	case c.Stage == "mtp":
		return "MTP-VPN напрямую (аварийный режим)…"
	case c.Stage == "ssh" || c.Stage == "up":
		return "Шаг 2–3 из 3: SSH-канал и MTP-VPN через него…"
	case len(need) == 0:
		return "Готово к подключению: AmneziaWG → SSH → MTP-VPN. Нажмите «Подключить»."
	case len(have) == 0:
		return "Сначала: загрузите «Поделиться» из Amnezia (.conf) и укажите данные SSH к своему серверу."
	default:
		return "Уже есть: " + strings.Join(have, ", ") + ". Не хватает: " + strings.Join(need, ", ") + "."
	}
}

// stateTexts собирает строки главного экрана из состояния программы.
// selectedName — название выбранного ключа (пустое, если ключ не выбран).
func stateTexts(st StateDTO, selectedName string, eff shownChoice) viewTexts {
	v := viewTexts{Title: phaseTitle(st.Phase, st.Message), Chain: chainText(st)}

	switch {
	case st.Session != nil:
		v.Key = "Ключ «" + st.Session.KeyName + "» · сервер " + st.Session.Host
	case selectedName != "":
		v.Key = "Выбран ключ «" + selectedName + "»"
	default:
		v.Key = "Ключ не выбран"
	}

	live := st.Session != nil && (st.Phase == "connected" || st.Phase == "connecting")
	var t, r, m string
	switch {
	case live:
		t, r, m = st.Session.TransportLabel, st.Session.RoutingLabel, st.Session.ModeLabel
	case selectedName != "" && eff.Error == "":
		t, r, m = eff.TransportLabel, eff.RoutingLabel, eff.ModeLabel
	case st.Session != nil:
		t, r, m = st.Session.TransportLabel, st.Session.RoutingLabel, st.Session.ModeLabel
	}
	switch {
	case t != "":
		v.Details = "Способ: " + t + " · " + r
		if m != "" && m != "Обычный" {
			v.Details += " · " + strings.ToLower(m) + " режим"
		}
	case selectedName != "" && eff.Error != "":
		v.Details = "Для этого ключа выбранный режим недоступен"
	default:
		v.Details = "Добавьте ключ, который вам прислали"
	}

	msg := ""
	if st.Phase == "error" || (st.Phase != "connected" && st.Message != "" &&
		st.Message != "Не подключено" && st.Message != "Отключено") {
		msg = st.Message
	}
	if msg == "" && st.Phase == "idle" && selectedName != "" && eff.Error != "" {
		msg = eff.Error
	}
	v.Notice = msg

	if st.Phase == "connected" {
		sec := (st.NowMs - st.StartedAt) / 1000
		v.Traffic = "Сеанс " + durationText(sec) + " · получено " + humanBytes(st.Rx) + " · отправлено " + humanBytes(st.Tx)
	}

	if st.Used != nil {
		var lim, exp int64
		if st.Limit != nil {
			lim = *st.Limit
		}
		if st.Expires != nil {
			exp = *st.Expires
		}
		v.Quota = "На сервере: " + quotaText(*st.Used, lim) + " · срок: " + dateText(exp)
	} else if st.Phase == "connected" && st.Session != nil && !strings.HasPrefix(st.Session.Transport, "MTP") {
		v.Quota = "Остаток на сервере виден при подключении по MTP"
	} else if st.Phase == "connected" {
		v.Quota = "Жду данные сервера…"
	}
	return v
}

// keyLine — строка ключа в списке: отметка, название, способ, маршрутизация, остаток.
func keyLine(sk SavedKey, host string, selected, active bool, phase string) string {
	mark := "○"
	switch {
	case active && phase == "connected":
		mark = "●"
	case active && phase == "connecting":
		mark = "…"
	case selected:
		mark = "›"
	}
	parts := []string{sk.DisplayName()}
	if host != "" {
		parts = append(parts, host)
	}
	if sk.Transport.Valid() {
		parts = append(parts, sk.Transport.Label())
	}
	if sk.Routing.Valid() {
		parts = append(parts, sk.Routing.Label())
	}
	line := mark + " " + strings.Join(parts, " · ")
	if sk.Stat != nil && sk.Stat.At > 0 {
		line += " · " + quotaText(sk.Stat.Used, sk.Stat.Limit) + " · до " + dateText(sk.Stat.Expires)
	}
	return line
}

// serverUserLine — строка ключа на сервере (вкладка «Сервер»).
func serverUserLine(u adminUser) string {
	mark := "·"
	switch u.Status {
	case "активен":
		mark = "›"
	case "отключён", "истёк", "лимит":
		mark = "×"
	}
	if u.Online {
		mark = "●"
	}
	parts := []string{u.Name}
	if u.IP != "" {
		parts = append(parts, u.IP)
	}
	if u.Status != "" {
		parts = append(parts, u.Status)
	}
	line := mark + " " + strings.Join(parts, " · ") + " · " + quotaText(u.Used, u.Limit) + " · до " + dateText(u.Expires)
	return line
}

// serverInfoText — строки про настроенный сервер.
func serverInfoText(info ServerInfo, configured bool) (title, details string) {
	if !configured {
		return "Сервер не настроен", "Нажмите «Настроить сервер»: программа сама поставит VPN на ваш сервер по SSH и создаст первый ключ"
	}
	title = "Сервер: " + info.Host
	details = fmt.Sprintf("вход %s@%s:%d · порт VPN %d", info.SSHUser, info.Host, info.SSHPort, info.VPNPort)
	if info.Users > 0 {
		details += fmt.Sprintf(" · ключей %d, сейчас в сети %d", info.Users, info.Online)
	}
	if info.Checked > 0 {
		details += " · проверка " + stampText(info.Checked)
	}
	if info.Error != "" {
		details += "\nОшибка: " + info.Error
	}
	return title, details
}

// keyStatText — сведения о трафике ключа (для окна настроек ключа).
func keyStatText(st *KeyStat) string {
	if st == nil || st.At == 0 {
		return "Сведений о трафике пока нет: они приходят от сервера во время подключения"
	}
	s := "Трафик: " + quotaText(st.Used, st.Limit) + " · срок: " + dateText(st.Expires) + " · данные на " + stampText(st.At)
	return s
}

// supportsText — какие способы есть в ключе, а каких нет.
func supportsText(key *ServerKey) string {
	var yes, no []string
	for _, t := range allTransports {
		if key.Supports(t) {
			yes = append(yes, t.Label())
		} else {
			no = append(no, t.Label())
		}
	}
	s := "Есть в ключе: " + strings.Join(yes, ", ")
	if len(no) > 0 {
		s += "\nНет в ключе: " + strings.Join(no, ", ")
	}
	return s
}
