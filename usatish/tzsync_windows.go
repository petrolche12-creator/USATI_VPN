//go:build windows

package main

// Синхронизация часового пояса с выходным сервером для анонимного режима.
// Проверки анонимности сравнивают часовой пояс устройства с географией IP:
// несовпадение — один из главных признаков «сидит через VPN». На время подключения
// ставим пояс сервера штатной утилитой tzutil и возвращаем исходный при отключении.

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// ianaToWindows — соответствие имён IANA именам часовых поясов Windows (частые локации).
var ianaToWindows = map[string]string{
	"Europe/Berlin":                  "W. Europe Standard Time",
	"Europe/Amsterdam":               "W. Europe Standard Time",
	"Europe/Brussels":                "W. Europe Standard Time",
	"Europe/Zurich":                  "W. Europe Standard Time",
	"Europe/Vienna":                  "W. Europe Standard Time",
	"Europe/Stockholm":               "W. Europe Standard Time",
	"Europe/Oslo":                    "W. Europe Standard Time",
	"Europe/Copenhagen":              "W. Europe Standard Time",
	"Europe/Paris":                   "Romance Standard Time",
	"Europe/Madrid":                  "Romance Standard Time",
	"Europe/London":                  "GMT Standard Time",
	"Europe/Lisbon":                  "GMT Standard Time",
	"Europe/Dublin":                  "GMT Standard Time",
	"Europe/Warsaw":                  "Central European Standard Time",
	"Europe/Prague":                  "Central European Standard Time",
	"Europe/Budapest":                "Central European Standard Time",
	"Europe/Zagreb":                  "Central European Standard Time",
	"Europe/Belgrade":                "Central Europe Standard Time",
	"Europe/Kyiv":                    "FLE Standard Time",
	"Europe/Kiev":                    "FLE Standard Time",
	"Europe/Helsinki":                "FLE Standard Time",
	"Europe/Vilnius":                 "FLE Standard Time",
	"Europe/Riga":                    "FLE Standard Time",
	"Europe/Sofia":                   "FLE Standard Time",
	"Europe/Athens":                  "GTB Standard Time",
	"Europe/Bucharest":               "GTB Standard Time",
	"Europe/Istanbul":                "Turkey Standard Time",
	"Europe/Minsk":                   "Belarus Standard Time",
	"Europe/Moscow":                  "Russian Standard Time",
	"Europe/Samara":                  "Russia Time Zone 3",
	"Asia/Yekaterinburg":             "Russia Time Zone 4",
	"Asia/Novosibirsk":               "N. Central Asia Standard Time",
	"Asia/Krasnoyarsk":               "North Asia Standard Time",
	"Asia/Irkutsk":                   "North Asia East Standard Time",
	"Asia/Vladivostok":               "Vladivostok Standard Time",
	"Asia/Kamchatka":                 "Russia Time Zone 11",
	"Asia/Almaty":                    "Central Asia Standard Time",
	"Asia/Tashkent":                  "West Asia Standard Time",
	"Asia/Baku":                      "Azerbaijan Standard Time",
	"Asia/Yerevan":                   "Caucasus Standard Time",
	"Asia/Tbilisi":                   "Georgian Standard Time",
	"Asia/Dubai":                     "Arabian Standard Time",
	"Asia/Riyadh":                    "Arab Standard Time",
	"Asia/Tehran":                    "Iran Standard Time",
	"Asia/Karachi":                   "Pakistan Standard Time",
	"Asia/Kolkata":                   "India Standard Time",
	"Asia/Bangkok":                   "SE Asia Standard Time",
	"Asia/Jakarta":                   "SE Asia Standard Time",
	"Asia/Singapore":                 "Singapore Standard Time",
	"Asia/Hong_Kong":                 "China Standard Time",
	"Asia/Shanghai":                  "China Standard Time",
	"Asia/Taipei":                    "Taipei Standard Time",
	"Asia/Seoul":                     "Korea Standard Time",
	"Asia/Tokyo":                     "Tokyo Standard Time",
	"America/New_York":               "Eastern Standard Time",
	"America/Toronto":                "Eastern Standard Time",
	"America/Chicago":                "Central Standard Time",
	"America/Mexico_City":            "Central America Standard Time",
	"America/Denver":                 "Mountain Standard Time",
	"America/Phoenix":                "US Mountain Standard Time",
	"America/Los_Angeles":            "Pacific Standard Time",
	"America/Vancouver":              "Pacific Standard Time",
	"America/Anchorage":              "Alaskan Standard Time",
	"Pacific/Honolulu":               "Hawaiian Standard Time",
	"Australia/Sydney":               "AUS Eastern Standard Time",
	"Australia/Melbourne":            "AUS Eastern Standard Time",
	"Australia/Perth":                "W. Australia Standard Time",
	"Pacific/Auckland":               "New Zealand Standard Time",
	"America/Sao_Paulo":              "E. South America Standard Time",
	"America/Argentina/Buenos_Aires": "Argentina Standard Time",
	"Africa/Cairo":                   "Egypt Standard Time",
	"Africa/Johannesburg":            "South Africa Standard Time",
	"Africa/Lagos":                   "W. Central Africa Standard Time",
	"Africa/Nairobi":                 "E. Africa Standard Time",
}

// langByIANA — список Accept-Language для стран частых серверов: язык браузера должен
// соглашаться с географией IP, иначе антифрод видит нестыковку.
var langByIANA = map[string]string{
	"Europe/Berlin":       "de-de, de, en-us, en",
	"Europe/Amsterdam":    "nl-nl, nl, en-us, en",
	"Europe/Vienna":       "de-de, de, en-us, en",
	"Europe/Zurich":       "de-ch, de, en-us, en",
	"Europe/Paris":        "fr-fr, fr, en-us, en",
	"Europe/Brussels":     "nl-be, fr-be, en-us, en",
	"Europe/London":       "en-gb, en",
	"Europe/Dublin":       "en-ie, en",
	"Europe/Madrid":       "es-es, es, en-us, en",
	"Europe/Lisbon":       "pt-pt, pt, en-us, en",
	"Europe/Warsaw":       "pl-pl, pl, en-us, en",
	"Europe/Prague":       "cs-cz, cs, en-us, en",
	"Europe/Stockholm":    "sv-se, sv, en-us, en",
	"Europe/Oslo":         "no-no, no, en-us, en",
	"Europe/Copenhagen":   "da-dk, da, en-us, en",
	"Europe/Helsinki":     "fi-fi, fi, en-us, en",
	"Europe/Riga":         "lv-lv, lv, en-us, en",
	"Europe/Vilnius":      "lt-lt, lt, en-us, en",
	"Europe/Kyiv":         "uk-ua, uk, en-us, en",
	"Europe/Istanbul":     "tr-tr, tr, en-us, en",
	"Europe/Moscow":       "ru-ru, ru, en-us, en",
	"America/New_York":    "en-us, en",
	"America/Chicago":     "en-us, en",
	"America/Denver":      "en-us, en",
	"America/Los_Angeles": "en-us, en",
	"America/Toronto":     "en-ca, en",
	"Asia/Tokyo":          "ja-jp, ja, en-us, en",
	"Asia/Seoul":          "ko-kr, ko, en-us, en",
	"Asia/Singapore":      "en-sg, en",
	"Australia/Sydney":    "en-au, en",
}

// fetchGeo возвращает часовой пояс сервера (имя Windows) и язык его страны (IANA-список).
func fetchGeo(host string) (win, lang string) {
	ip, err := resolveIPv4(host)
	if err != nil {
		return "", ""
	}
	client := &http.Client{Timeout: 6 * time.Second}
	for _, u := range []string{
		"https://ipapi.co/" + ip.String() + "/timezone",
		"http://ip-api.com/line/" + ip.String() + "?fields=timezone",
	} {
		resp, err := client.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		line := strings.TrimSpace(string(b))
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = line[:i]
		}
		w, ok := ianaToWindows[line]
		if !ok {
			continue
		}
		return w, langByIANA[line]
	}
	return "", ""
}

func currentTZ() string {
	out, err := runNet("tzutil", "/g")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func setTZ(name string) error {
	_, err := runNet("tzutil", "/s", name)
	return err
}

// applyTimezone ставляет часовой пояс win, если он отличается от текущего.
// Возвращает исходный пояс и успех; если пояс уже совпадает или пуст — ничего не трогает.
func applyTimezone(win string) (orig string, ok bool) {
	if win == "" {
		return "", false
	}
	orig = currentTZ()
	if orig == "" || orig == win {
		return "", false
	}
	if err := setTZ(win); err != nil {
		return "", false
	}
	return orig, true
}
