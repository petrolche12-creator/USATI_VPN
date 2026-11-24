//go:build windows

package main

// Приватность на уровне системы пока поднят туннель.
//
// Проверки анонимности снижают оценку не за протокол в проводе, а за утечки:
//   - DNS уходит к серверам провайдера (система держит их на физическом адаптере,
//     даже когда весь трафик идёт через VPN) — сайты видят «DNS вашего провайдера»
//     и понимают, что туннель не полный;
//   - IPv6-запросы минуют туннель;
//   - часовой пояс компьютера не совпадает с географией выходного IP.
//
// На время подключения мы перекрываем DNS на физических адаптерах тем же адресом,
// что использует туннель, в анонимном режиме отключаем IPv6 на физических адаптерах
// и ставим часовой пояс как на сервере. При отключении всё возвращается; если программу
// убили, восстановление выполняется при следующем запуске по файлу net-restore.json.

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type dnsPrev struct {
	Name string   `json:"name"`          // имя адаптера
	DHCP bool     `json:"dhcp"`          // раньше DNS приходили по DHCP
	IPs  []string `json:"ips,omitempty"` // прежние статические DNS (если были)
}

type privacyState struct {
	DNS    []dnsPrev `json:"dns,omitempty"`    // адаптеры, где DNS перекрыт на туннельный
	IPv6   []string  `json:"ipv6,omitempty"`   // адаптеры, где IPv6 временно отключён
	TZ     string    `json:"tz,omitempty"`     // исходный часовой пояс Windows
	WebRTC bool      `json:"webrtc,omitempty"` // мы ставили политику браузера против WebRTC
	Lang   bool      `json:"lang,omitempty"`   // мы меняли Accept-Language в Firefox
	NRPT   bool      `json:"nrpt,omitempty"`   // игровой режим: RU-домены резолвились напрямую
}

// nrptRuleComment — метка нашего правила, по нему же правило удаляется.
const nrptRuleComment = "UsatyiVPN-game"

// addNrptRuDirect: в игровом режиме российские домены резолвятся DNS-серверами провайдера
// напрямую, без петли через туннель (+40 мс на каждый первый запрос). Утечки тут нет:
// соединения к российским адресам в игровом режиме и так идут напрямую.
func addNrptRuDirect(servers []string) error {
	seen := map[string]bool{}
	var ns []string
	for _, s := range servers {
		if s != "" && !seen[s] {
			seen[s] = true
			ns = append(ns, s)
		}
		if len(ns) == 2 {
			break
		}
	}
	if len(ns) == 0 {
		return errors.New("нет DNS-адресов провайдера")
	}
	_, err := runNet("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Add-DnsClientNrptRule -Namespace '.ru','.рф','.su','.moscow','.tatar' "+
			"-NameServers '"+strings.Join(ns, ",")+"' -Comment '"+nrptRuleComment+"' -Force")
	return err
}

func removeNrptRuDirect() {
	_, _ = runNet("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-DnsClientNrptRule | Where-Object {$_.Comment -eq '"+nrptRuleComment+"'} | "+
			"Remove-DnsClientNrptRule -Force")
}

// firefoxLangPref — раздел политики Firefox, где живёт список языков браузера.
const firefoxLangPref = `HKLM\Software\Policies\Mozilla\Firefox\Preferences`

func setFirefoxLanguage(lang string) error {
	_, err := runNet("reg", "add", firefoxLangPref, "/v", "intl.accept_languages", "/t", "REG_SZ",
		"/d", `"`+lang+`"`, "/f")
	return err
}

func resetFirefoxLanguage() {
	_, _ = runNet("reg", "delete", firefoxLangPref, "/v", "intl.accept_languages", "/f")
}

// webrtcPolicyKeys — разделы реестра корпоративных политик Chromium-браузеров.
// Политика WebRtcIPHandlingPolicy=disable_non_proxied_udp запрещает WebRTC ходить в сеть
// мимо прокси; прокси в браузере нет, значит STUN-запросы и утечки IP исчезают. Сам API
// WebRTC из браузера не убирается (такое умеют только расширения), но сеть для него
// закрыта: проверки не видят ни публичных, ни локальных адресов.
var webrtcPolicyKeys = []string{
	`HKLM\Software\Policies\Google\Chrome`,
	`HKLM\Software\Policies\Microsoft\Edge`,
	`HKLM\Software\Policies\Yandex\YandexBrowser`,
	`HKLM\Software\Policies\BraveSoftware\Brave`,
}

// setWebRTCPolicy ставит или снимает политику; возвращает число затронутых разделов.
func setWebRTCPolicy(on bool) int {
	done := 0
	for _, k := range webrtcPolicyKeys {
		var err error
		if on {
			_, err = runNet("reg", "add", k, "/v", "WebRtcIPHandlingPolicy", "/t", "REG_SZ",
				"/d", "disable_non_proxied_udp", "/f")
		} else {
			_, err = runNet("reg", "delete", k, "/v", "WebRtcIPHandlingPolicy", "/f")
		}
		if err == nil {
			done++
		}
	}
	return done
}

var (
	privMu   sync.Mutex
	privDir  string
	privLive privacyState
)

func privFile() string { return filepath.Join(privDir, "net-restore.json") }

func privSave(st privacyState) {
	b, _ := json.Marshal(st)
	_ = os.WriteFile(privFile(), b, 0o600)
}

func privLoad() (privacyState, bool) {
	var st privacyState
	b, err := os.ReadFile(privFile())
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	return st, true
}

// upAdaptersWithDNS — все работающие адаптеры и их прежняя настройка DNS.
// Берём именно все (а не только физические): виртуальные адаптеры Hyper-V, WSL, TAP
// тоже держат DNS провайдера, и один запрос туда выглядит для проверок как утечка.
// Наши туннели исключены: их DNS задаёт сам туннель.
func upAdaptersWithDNS() []dnsPrev {
	out, err := runNet("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-NetAdapter | Where-Object {$_.Status -eq 'Up'} | ForEach-Object { "+
			"$c = Get-CimInstance Win32_NetworkAdapterConfiguration -Filter (\"InterfaceIndex=\" + $_.InterfaceIndex); "+
			"$d = if ($c.DHCPServer) {'1'} else {'0'}; "+
			"Write-Output ($_.Name + [char]9 + $d + [char]9 + (($c.DNSServerSearchOrder) -join '|')) }")
	if err != nil {
		return nil
	}
	var res []dnsPrev
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 || f[0] == "" {
			continue
		}
		if f[0] == "UsatyiVPN" || f[0] == "UsatyiWG" {
			continue
		}
		p := dnsPrev{Name: f[0], DHCP: f[1] == "1"}
		if len(f) == 3 && f[2] != "" {
			p.IPs = strings.Split(f[2], "|")
		}
		res = append(res, p)
	}
	return res
}

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func setAdapterDNS(name, ip string) error {
	_, err := runNet("netsh", "interface", "ip", "set", "dns", "name="+name, "static", ip, "primary")
	return err
}

func setAdapterDNSDHCP(name string) error {
	_, err := runNet("netsh", "interface", "ip", "set", "dns", "name="+name, "dhcp")
	return err
}

func setIPv6Binding(name string, enable bool) error {
	verb := "Disable-NetAdapterBinding"
	if enable {
		verb = "Enable-NetAdapterBinding"
	}
	_, err := runNet("powershell", "-NoProfile", "-NonInteractive", "-Command",
		verb+" -Name "+psQuote(name)+" -ComponentID ms_tcpip6")
	return err
}

// tunUpPrivacy вызывается сразу после поднятия туннеля.
func tunUpPrivacy(dir string, logf func(string, ...any), dns net.IP, mode Mode, host string) {
	anon := mode == ModeAnon
	privMu.Lock()
	defer privMu.Unlock()
	privDir = dir
	st := privacyState{}
	// Если с прошлого раза остался несохранённый сдвиг пояса (программу убили, отказ
	// в правах) — чиним сразу и запоминаем домашний пояс, чтобы не потерять его снова.
	if old, ok := privLoad(); ok && old.TZ != "" {
		cur := currentTZ()
		if cur != "" && cur != old.TZ {
			if isAdmin() {
				if err := setTZ(old.TZ); err == nil {
					logf("возвращаю домашний часовой пояс после прошлого сеанса: %s", old.TZ)
					cur = old.TZ
				}
			}
			if cur != old.TZ {
				logf("ВНИМАНИЕ: часовой пояс системы %s не совпадает с домашним %s — верните его вручную один раз", cur, old.TZ)
			}
			st.TZ = old.TZ
		}
	}
	ips := "1.1.1.1"
	if v4 := dns.To4(); v4 != nil {
		ips = v4.String()
	}
	ads := upAdaptersWithDNS()
	if mode == ModeGame {
		var prov []string
		for _, a := range ads {
			prov = append(prov, a.IPs...)
		}
		if err := addNrptRuDirect(prov); err == nil {
			st.NRPT = true
			logf("игровой режим: российские домены резолвятся напрямую у провайдера, без петли через туннель")
		} else {
			logf("игровой режим: NRPT-сплит DNS не применился: %v", err)
		}
	}
	for _, a := range ads {
		if err := setAdapterDNS(a.Name, ips); err == nil {
			st.DNS = append(st.DNS, a)
		} else {
			logf("не удалось перекрыть DNS на адаптере %s: %v", a.Name, err)
		}
		if err := setIPv6Binding(a.Name, false); err == nil {
			st.IPv6 = append(st.IPv6, a.Name)
		} else {
			logf("не удалось отключить IPv6 на адаптере %s: %v", a.Name, err)
		}
	}
	if len(st.IPv6) > 0 {
		logf("IPv6 отключён на всех адаптерах на время подключения: туннель IPv4-only, иначе IPv6 и IPv6-DNS идут мимо VPN")
	}
	if n := setWebRTCPolicy(true); n > 0 {
		st.WebRTC = true
		logf("WebRTC: политика disable_non_proxied_udp установлена для %d Chromium-браузеров; перезапустите браузер один раз", n)
	} else {
		logf("WebRTC: политику браузеров поставить не удалось (нужны права администратора)")
	}
	if len(st.DNS) > 0 {
		names := make([]string, 0, len(st.DNS))
		for _, d := range st.DNS {
			names = append(names, d.Name)
		}
		logf("DNS перекрыт на всех адаптерах (%s): провайдерские DNS больше не используются", strings.Join(names, ", "))
	}
	if anon && len(st.IPv6) > 0 {
		logf("анонимный режим: IPv6 на физических адаптерах отключён на время подключения")
	}
	if anon {
		win, lang := fetchGeo(host)
		if lang != "" {
			if err := setFirefoxLanguage(lang); err == nil {
				st.Lang = true
				logf("язык браузера (Firefox) на время подключения: %s — как в стране сервера", lang)
			} else {
				logf("язык браузера поменять не удалось (нужны права администратора): %v", err)
			}
		}
		if orig, ok := applyTimezone(win); ok {
			st.TZ = orig
			logf("анонимный режим: часовой пояс компьютера временно «%s», как у сервера; вернётся при отключении", win)
		} else if win == "" {
			logf("анонимный режим: географию сервера определить не удалось, пояс и язык оставляю как есть")
		}
	}
	privLive = st
	if len(st.DNS) > 0 || len(st.IPv6) > 0 || st.TZ != "" {
		privSave(st)
	}
	auditDNS(logf, ips)
}

// auditDNS перечитывает эффективные DNS-серверы системы после перекрытия и раскладывает
// их по журналу: если где-то остался чужой сервер, это видно сразу, без_guesswork.
func auditDNS(logf func(string, ...any), expect string) {
	out, err := runNet("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"Get-DnsClientServerAddress -AddressFamily IPv4 | Where-Object {$_.ServerAddresses} | "+
			"ForEach-Object { $_.InterfaceAlias + [char]9 + ($_.ServerAddresses -join '|') }")
	if err != nil {
		logf("самопроверка DNS: не удалось прочитать настройки системы: %v", err)
		return
	}
	strange := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 2)
		if len(f) < 2 {
			continue
		}
		logf("самопроверка DNS: %s = %s", f[0], f[1])
		for _, a := range strings.Split(f[1], "|") {
			if a != expect && a != "" {
				strange++
			}
		}
	}
	if strange == 0 {
		logf("самопроверка DNS: в системе только туннельный DNS, утечек нет")
	} else {
		logf("самопроверка DNS: ВНИМАНИЕ, в системе осталось чужих DNS: %d — пришли журнал разработчику", strange)
	}
}

// tunDownPrivacy возвращает всё обратно при отключении.
func tunDownPrivacy(logf func(string, ...any)) {
	privMu.Lock()
	defer privMu.Unlock()
	st := privLive
	privLive = privacyState{}
	if restorePrivacy(st, logf) {
		_ = os.Remove(privFile())
	}
}

// recoverPrivacy — восстановление после аварийного выхода (файл остался с прошлого раза).
func recoverPrivacy(dir string, logf func(string, ...any)) {
	privMu.Lock()
	defer privMu.Unlock()
	privDir = dir
	st, ok := privLoad()
	if !ok {
		return
	}
	logf("нашёл несохранённое восстановление сети после прошлого выхода, возвращаю настройки")
	if restorePrivacy(st, logf) {
		_ = os.Remove(privFile())
	} else {
		logf("часть настроек вернуть не удалось (нужны права администратора): попробую при следующем подключении")
	}
}

// restorePrivacy применяет обратные изменения; false, если что-то не получилось.
func restorePrivacy(st privacyState, logf func(string, ...any)) bool {
	ok := true
	for _, a := range st.DNS {
		var err error
		if a.DHCP || len(a.IPs) == 0 {
			err = setAdapterDNSDHCP(a.Name)
		} else {
			err = setAdapterDNS(a.Name, a.IPs[0])
			for i, extra := range a.IPs[1:] {
				_, _ = runNet("netsh", "interface", "ip", "add", "dns", "name="+a.Name, extra, strconv.Itoa(i+2))
			}
		}
		if err != nil {
			ok = false
			logf("не удалось вернуть DNS на адаптере %s: %v", a.Name, err)
		}
	}
	for _, a := range st.IPv6 {
		if err := setIPv6Binding(a, true); err != nil {
			ok = false
			logf("не удалось включить IPv6 на адаптере %s: %v", a, err)
		}
	}
	if st.TZ != "" {
		if err := setTZ(st.TZ); err != nil {
			ok = false
			logf("не удалось вернуть часовой пояс %s: %v", st.TZ, err)
		} else {
			logf("часовой пояс возвращён: %s", st.TZ)
		}
	}
	if st.WebRTC {
		setWebRTCPolicy(false)
		logf("WebRTC: политика браузеров снята, WebRTC снова работает")
	}
	if st.Lang {
		resetFirefoxLanguage()
		logf("язык браузера (Firefox) возвращён")
	}
	if st.NRPT {
		removeNrptRuDirect()
		logf("игровой режим: сплит DNS для российских доменов снят")
	}
	if len(st.DNS) > 0 || len(st.IPv6) > 0 || st.TZ != "" {
		if ok {
			logf("сетевые настройки компьютера восстановлены после отключения")
		}
	}
	return ok
}
