//go:build windows

package main

// Действия нативного окна: нажатия кнопок, обновление состояния, фоновые операции.
// Всё вызывает методы App напрямую — никаких сетевых запросов к самому себе.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Сообщения от элементов окна
// ---------------------------------------------------------------------------

func (g *gui) onCommand(wp, lp uintptr) {
	code := int((wp >> 16) & 0xFFFF)
	id := int(wp & 0xFFFF)
	if g.isModal() && id != 0 {
		// Пока открыто окно ввода, главное окно недоступно.
		return
	}
	switch id {
	case idTabConnect, idTabServer, idTabSettings:
		page := pageConnect
		switch id {
		case idTabServer:
			page = pageServer
		case idTabSettings:
			page = pageSettings
		}
		if page != g.page {
			g.page = page
			g.layout()
			g.refresh()
			if page == pageServer {
				g.refreshServerList(true)
			}
		}
		return

	case idKeysList:
		if code == lnSelChange {
			g.selectKeyFromList()
		}
		return
	case idSrvList:
		return
	case idProtoList:
		if code == lnSelChange {
			g.saveProtocol()
		}
		return
	case idModeGame, idModeNormal, idModeAnon:
		if code == bnClicked {
			g.saveMode()
		}
		return
	case idAdvCheck:
		if code == bnClicked {
			g.saveAdvanced()
		}
		return
	case idAwgAsVpn, idMTPDirect:
		if code == bnClicked {
			g.saveAmneziaOptions()
		}
		return

	case idBtnConnect:
		g.actConnect()
	case idBtnDisconn:
		g.actDisconnect()
	case idBtnAddKey:
		g.actAddKey()
	case idBtnKeyOpts:
		g.actKeySettings()
	case idBtnKeyDel:
		g.actKeyDelete()
	case idBtnCopySS:
		g.actCopySS()
	case idBtnConf:
		g.actLoadConf()
	case idBtnConfForget:
		g.actForgetConf()
	case idBtnAwgSetup:
		g.actInstallAmneziaWG()

	case idBtnSrvSetup:
		g.actServerSetup()
	case idBtnSrvCheck:
		g.actServerCheck()
	case idBtnSrvForget:
		g.actServerForget()
	case idBtnSrvNew:
		g.actServerNewKey()
	case idBtnSrvShow:
		g.actServerShowKey()
	case idBtnSrvToggle:
		g.actServerToggle()
	case idBtnSrvReset:
		g.actServerReset()
	case idBtnSrvDel:
		g.actServerDelete()

	case idBtnQuit:
		g.quit(true)
	}
}

func (g *gui) isModal() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.modal
}

// quit закрывает окно: сначала спрашивает, если VPN ещё работает.
func (g *gui) quit(ask bool) {
	g.mu.Lock()
	if g.quitting {
		g.mu.Unlock()
		return
	}
	g.mu.Unlock()

	st := g.app.State()
	if ask && (st.Phase == "connected" || st.Phase == "connecting") {
		if !askYesNo(g.hwnd, "Усатый VPN",
			"VPN сейчас работает. Отключить VPN и закрыть программу?") {
			return
		}
	}
	g.log("выход")
	g.app.Disconnect()
	g.app.Quit()
	pDestroyWindow.Call(g.hwnd)
}

// ---------------------------------------------------------------------------
// Страница «Подключение»
// ---------------------------------------------------------------------------

func (g *gui) selectKeyFromList() {
	sel := listBoxSel(g.ctlOf(idKeysList))
	if sel < 0 || sel >= len(g.keyIDs) {
		return
	}
	id := g.keyIDs[sel]
	if err := g.app.keys.Select(id); err != nil {
		g.log("не удалось выбрать ключ: %v", err)
		return
	}
	g.refresh()
}

func (g *gui) selectedKeyID() string {
	sel := listBoxSel(g.ctlOf(idKeysList))
	if sel >= 0 && sel < len(g.keyIDs) {
		return g.keyIDs[sel]
	}
	return g.app.keys.Selected()
}

func (g *gui) actConnect() {
	id := g.selectedKeyID()
	if id == "" {
		alert(g.hwnd, "Усатый VPN", "Сначала добавьте ключ и выберите его в списке.", false)
		return
	}
	if !isAdmin() {
		// VPN поднимает адаптер и маршруты: нужны права администратора. Окно при этом
		// уже открыто без прав — просим повышение и передаём ключ новой копии программы.
		g.log("для подключения нужны права администратора: Windows сейчас спросит подтверждение")
		if relaunchElevated(g.dir, id) {
			g.app.Shutdown()
			os.Exit(0)
		}
		g.log("повышение прав не удалось: запрос Windows отклонён или заблокирован")
		alert(g.hwnd, "Нужны права администратора",
			"Windows не повысила права (запрос отклонён или заблокирован).\n"+
				"Закройте программу и запустите её правой кнопкой — «Запуск от имени администратора».", true)
		g.refresh()
		return
	}
	g.connectKey(id)
}

// connectKey выбирает ключ и начинает подключение; вызывается только с нужными правами.
func (g *gui) connectKey(id string) {
	if err := ensureWintun(g.dir); err != nil {
		g.log("драйвер WinTun не загрузился: %v", err)
		alert(g.hwnd, "Не получилось подключиться",
			"Не удалось подготовить сетевой драйвер WinTun: "+err.Error(), true)
		return
	}
	if err := g.app.keys.Select(id); err != nil {
		g.log("%v", err)
	}
	if err := g.app.Connect(id); err != nil {
		alert(g.hwnd, "Не получилось подключиться", err.Error(), true)
		g.log("подключение не началось: %v", err)
		g.refresh()
		return
	}
	g.refresh()
}

func (g *gui) actDisconnect() {
	g.app.Disconnect()
	g.log("отключаю VPN")
	g.refresh()
}

func (g *gui) actAddKey() {
	text := ""
	for {
		res := g.showForm("Добавить ключ",
			"Вставьте строку ключа, которую вам прислали: mtp://… (или ссылку WireGuard / Shadowsocks).\n"+
				"Ключ сохраняется только на этом компьютере.",
			[]formField{{label: "Ключ:", value: text, multiline: true}}, "Добавить")
		if !res.ok {
			return
		}
		text = strings.TrimSpace(res.values[0])
		if text == "" {
			alert(g.hwnd, "Усатый VPN", "Поле пустое: вставьте строку ключа.", false)
			continue
		}
		key, err := ParseKey(text)
		if err != nil {
			alert(g.hwnd, "Это не похоже на ключ", err.Error(), true)
			continue
		}
		sk, created, replaced, err := g.app.keys.AddOrUpdate(text)
		if err != nil {
			alert(g.hwnd, "Не удалось сохранить ключ", err.Error(), true)
			continue
		}
		if err := g.app.keys.Select(sk.ID); err != nil {
			g.log("%v", err)
		}
		switch {
		case created:
			g.log("ключ «%s» добавлен (сервер %s)", sk.DisplayName(), key.Host)
		case replaced:
			g.log("ключ «%s» заменён на новый (сервер %s)", sk.DisplayName(), key.Host)
		default:
			g.log("ключ «%s» уже был в списке — выбран", sk.DisplayName())
		}
		g.refresh()
		g.showText("Ключ добавлен", "Что в этом ключе:", keyInfoText(key, sk))
		return
	}
}

func (g *gui) actKeySettings() {
	id := g.selectedKeyID()
	sk, ok := g.app.keys.Find(id)
	if !ok {
		alert(g.hwnd, "Усатый VPN", "Выберите ключ в списке.", false)
		return
	}
	key, err := ParseKey(sk.Text)
	if err != nil {
		alert(g.hwnd, "Усатый VPN", "Ключ не читается: "+err.Error(), true)
		return
	}
	tLabels, tHints, tSel := transportOptions(key, sk.Transport)
	rLabels, rHints, rSel := routingOptions(sk.Routing)

	res := g.showForm("Настройки ключа «"+sk.DisplayName()+"»", keyInfoText(key, sk),
		[]formField{
			{label: "Название (как вам удобно):", value: sk.Alias},
			{label: "Способ подключения:", options: tLabels, optHints: tHints, sel: tSel},
			{label: "Какой трафик идёт через VPN:", options: rLabels, optHints: rHints, sel: rSel},
		}, "Сохранить")
	if !res.ok {
		return
	}
	sk.Alias = strings.TrimSpace(res.values[0])
	sk.Transport = allTransports[res.sels[1]]
	sk.Routing = routingsForUI()[res.sels[2]]
	if err := g.app.keys.Update(sk); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	g.log("ключ «%s»: способ %s, трафик %s", sk.DisplayName(), sk.Transport.Label(), sk.Routing.Label())
	g.refresh()
}

func (g *gui) actKeyDelete() {
	id := g.selectedKeyID()
	sk, ok := g.app.keys.Find(id)
	if !ok {
		alert(g.hwnd, "Усатый VPN", "Выберите ключ в списке.", false)
		return
	}
	if g.app.activeKeyID() == id {
		alert(g.hwnd, "Усатый VPN", "Этот ключ сейчас работает. Сначала отключите VPN.", false)
		return
	}
	if !askYesNo(g.hwnd, "Удалить ключ",
		"Удалить ключ «"+sk.DisplayName()+"» с этого компьютера?\n"+
			"На сервере ключ останется: его можно удалить на вкладке «Сервер».") {
		return
	}
	if err := g.app.keys.Remove(id); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	g.log("ключ «%s» удалён с этого компьютера", sk.DisplayName())
	g.refresh()
}

func (g *gui) actCopySS() {
	id := g.selectedKeyID()
	sk, ok := g.app.keys.Find(id)
	if !ok {
		alert(g.hwnd, "Усатый VPN", "Выберите ключ в списке.", false)
		return
	}
	key, err := ParseKey(sk.Text)
	if err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	link, ok := SSLink(key)
	if !ok {
		alert(g.hwnd, "Усатый VPN", "В этом ключе нет параметров Shadowsocks.", false)
		return
	}
	if copyText(g.hwnd, link) {
		g.log("ссылка ss:// для ключа «%s» скопирована", sk.DisplayName())
		g.showText("Ссылка Shadowsocks", "Ссылка в буфере обмена. Вставьте её в приложение Shadowsocks.", link)
	} else {
		g.showText("Ссылка Shadowsocks", "Скопировать не удалось — выделите текст вручную.", link)
	}
}

// ---------------------------------------------------------------------------
// Страница «Сервер»
// ---------------------------------------------------------------------------

// actLoadConf — «Поделиться» из Amnezia: выбрать файл .conf и загрузить его.
func (g *gui) actLoadConf() {
	path := openConfDialog(g.hwnd)
	if path == "" {
		return
	}
	info, err := g.app.LoadAmneziaFile(path)
	if err != nil {
		alert(g.hwnd, "Усатый VPN", "Файл .conf не подошёл: "+err.Error(), true)
		return
	}
	g.log("загружен конфиг Amnezia «%s» (%s), сервер %s", info.Name, info.Kind, info.Endpoint)
	if !strings.Contains(strings.ToLower(info.Kind), "amnezia") {
		alert(g.hwnd, "Усатый VPN",
			"Это обычный WireGuard-конфиг без обфускации AmneziaWG.\nОн подойдёт, только если сервер Amnezia настроен без параметров Jc/S1/H1.", false)
	}
	g.refresh()
}

// actForgetConf — убрать сохранённый .conf с компьютера.
func (g *gui) actForgetConf() {
	if g.app.AmneziaStatus().Loaded == false {
		alert(g.hwnd, "Усатый VPN", "Файл .conf ещё не загружен.", false)
		return
	}
	if !askYesNo(g.hwnd, "Убрать .conf",
		"Убрать файл .conf Amnezia с этого компьютера?\nСам сервер и ключи MTP не изменятся.") {
		return
	}
	if err := g.app.ForgetAmneziaFile(); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	g.refresh()
}

// actInstallAmneziaWG — поставить AmneziaWG на свой сервер и сразу получить .conf.
// Нужен только если файла «Поделиться» из Amnezia ещё нет.
func (g *gui) actInstallAmneziaWG() {
	if g.app.Owner() == nil {
		alert(g.hwnd, "Усатый VPN", "Сначала настройте сервер на вкладке «Сервер»: IP, пользователь, пароль SSH, порт.", false)
		return
	}
	if !askYesNo(g.hwnd, "AmneziaWG на сервер",
		"Поставить на сервер AmneziaWG и создать файл .conf?\n"+
			"Нужен вход по SSH. Если AmneziaWG уже стоит, он будет перенастроен, а старые .conf перестанут работать.\n"+
			"Готовый .conf появится здесь, ставить клиент Amnezia на компьютер не нужно.") {
		return
	}
	if err := g.app.InstallAmneziaWG(); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	g.log("установка AmneziaWG на сервер начата: следите за журналом внизу")
}

// saveAmneziaOptions — галочки «весь трафик в AmneziaWG» и «аварийный режим».
func (g *gui) saveAmneziaOptions() {
	s := g.currentSettings()
	s.AmneziaAsVpn = checked(g.ctlOf(idAwgAsVpn))
	s.MTPDirect = checked(g.ctlOf(idMTPDirect))
	g.saveSettings(s)
}

func (g *gui) actServerSetup() {
	prev := g.app.Owner()
	host, user, sshPort, vpnPort, pass := "", "root", "22", "443", ""
	if prev != nil {
		host, user = prev.Host, prev.SSHUser
		sshPort, vpnPort = strconv.Itoa(prev.SSHPort), strconv.Itoa(prev.VPNPort)
	}
	for {
		hint := "Программа сама поставит VPN на ваш сервер: зайдёт по SSH, скопирует mtp.py, настроит systemd и создаст первый ключ.\n" +
			"Ничего вручную на сервере делать не нужно."
		if prev != nil {
			hint = "Сервер уже настроен: " + prev.Host + ".\nИзмените поля и нажмите «Установить заново», если нужно переустановить VPN."
		}
		res := g.showForm("Свой сервер", hint, []formField{
			{label: "IP или домен сервера:", value: host},
			{label: "Пользователь SSH:", value: user},
			{label: "Порт SSH:", value: sshPort},
			{label: "Пароль SSH:", value: pass, password: true},
			{label: "Порт VPN (UDP):", value: vpnPort},
		}, "Установить")
		if !res.ok {
			return
		}
		host, user, sshPort, pass, vpnPort = res.values[0], res.values[1], res.values[2], res.values[3], res.values[4]
		sp, err1 := strconv.Atoi(strings.TrimSpace(sshPort))
		vp, err2 := strconv.Atoi(strings.TrimSpace(vpnPort))
		if err1 != nil || err2 != nil {
			alert(g.hwnd, "Усатый VPN", "Порты — это числа: SSH "+sshPort+", VPN "+vpnPort+".", true)
			continue
		}
		form, err := buildOwnerForm(host, user, sp, pass, vp, prev)
		if err != nil {
			alert(g.hwnd, "Усатый VPN", err.Error(), true)
			continue
		}
		if err := g.app.InstallServer(form); err != nil {
			alert(g.hwnd, "Усатый VPN", err.Error(), true)
			continue
		}
		g.log("установка сервера %s начата: следите за журналом внизу", form.Host)
		return
	}
}

func (g *gui) actServerCheck() {
	var list *adminList
	g.runAsync("Проверяю сервер", func() (string, error) {
		l, err := g.app.ServerKeys()
		if err != nil {
			return "", err
		}
		list = l
		online := 0
		for _, u := range l.Users {
			if u.Online {
				online++
			}
		}
		return fmt.Sprintf("сервер %s отвечает: ключей %d, сейчас в сети %d", l.Host, len(l.Users), online), nil
	}, func() {
		if list != nil {
			g.srvUsers = list.Users
			g.refreshServerList(false)
		}
	})
}

func (g *gui) actServerForget() {
	if g.app.Owner() == nil {
		alert(g.hwnd, "Усатый VPN", "Сервер на этом компьютере не сохранён.", false)
		return
	}
	if !askYesNo(g.hwnd, "Забыть сервер",
		"Забыть сервер на этом компьютере?\nСам сервер продолжит работать, ключи останутся действительными.") {
		return
	}
	if err := g.app.ForgetOwner(); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		return
	}
	g.srvUsers = nil
	g.log("сервер забыт на этом компьютере")
	g.refresh()
}

func (g *gui) serverOwner() (*Owner, bool) {
	o := g.app.Owner()
	if o == nil {
		alert(g.hwnd, "Усатый VPN", "Сначала настройте сервер на вкладке «Сервер».", false)
		return nil, false
	}
	return o, true
}

func (g *gui) selectedServerUser() (adminUser, bool) {
	sel := listBoxSel(g.ctlOf(idSrvList))
	if sel < 0 || sel >= len(g.srvUsers) {
		alert(g.hwnd, "Усатый VPN", "Выберите ключ в списке.", false)
		return adminUser{}, false
	}
	return g.srvUsers[sel], true
}

func (g *gui) actServerNewKey() {
	o, ok := g.serverOwner()
	if !ok {
		return
	}
	for {
		res := g.showForm("Создать ключ", "Лимит и срок проверяет сервер: исчерпанный или истёкший ключ перестаёт работать сразу.",
			[]formField{
				{label: "Имя ключа (например, «Маша»):", value: "ключ"},
				{label: "Срок в днях (0 — без срока):", value: "30"},
				{label: "Лимит трафика в ГБ (0 — без лимита):", value: "10"},
			}, "Создать")
		if !res.ok {
			return
		}
		name := strings.TrimSpace(res.values[0])
		days, err1 := strconv.Atoi(strings.TrimSpace(res.values[1]))
		limit, err2 := strconv.ParseFloat(strings.Replace(strings.TrimSpace(res.values[2]), ",", ".", 1), 64)
		if !nameOK(name) {
			alert(g.hwnd, "Усатый VPN", "Имя ключа: от 1 до 40 символов.", true)
			continue
		}
		if err1 != nil || days < 0 || days > 3650 {
			alert(g.hwnd, "Усатый VPN", "Срок: число от 0 до 3650 дней, 0 — без срока.", true)
			continue
		}
		if err2 != nil || limit < 0 || limit > 100000 {
			alert(g.hwnd, "Усатый VPN", "Лимит: число ГБ, 0 — без лимита.", true)
			continue
		}
		var keyText string
		g.runAsync("Создаю ключ «"+name+"»", func() (string, error) {
			k, err := adminAddKey(o, name, days, limit)
			if err != nil {
				return "", err
			}
			keyText = k
			return "ключ «" + name + "» создан на сервере", nil
		}, func() {
			if keyText == "" {
				return
			}
			g.showText("Ключ «"+name+"» создан",
				"Отправьте этот ключ тому, кому он предназначен. Он сохраняется только на сервере и на этом компьютере.", keyText)
			if askYesNo(g.hwnd, "Усатый VPN", "Добавить этот ключ и в список на этом компьютере?") {
				if sk, _, _, err := g.app.keys.AddOrUpdate(keyText); err == nil {
					_ = g.app.keys.Select(sk.ID)
					g.log("ключ «%s» добавлен на этом компьютере", sk.DisplayName())
				} else {
					g.log("ключ не добавился в список: %v", err)
				}
				g.refresh()
			}
			g.actServerCheck()
		})
		return
	}
}

func (g *gui) actServerShowKey() {
	o, ok := g.serverOwner()
	if !ok {
		return
	}
	u, ok := g.selectedServerUser()
	if !ok {
		return
	}
	var keyText string
	g.runAsync("Запрашиваю ключ «"+u.Name+"»", func() (string, error) {
		k, err := adminShowKey(o, u.Name)
		if err != nil {
			return "", err
		}
		keyText = k
		return "ключ «" + u.Name + "» получен с сервера", nil
	}, func() {
		if keyText == "" {
			return
		}
		g.showText("Ключ «"+u.Name+"»", "Строка ключа с сервера:", keyText)
	})
}

func (g *gui) actServerToggle() {
	o, ok := g.serverOwner()
	if !ok {
		return
	}
	u, ok := g.selectedServerUser()
	if !ok {
		return
	}
	enable := u.Status != "активен"
	what := "отключаю"
	if enable {
		what = "включаю"
	}
	g.runAsync(capText(what)+" ключ «"+u.Name+"»", func() (string, error) {
		if err := adminSetEnabled(o, u.Name, enable); err != nil {
			return "", err
		}
		if enable {
			return "ключ «" + u.Name + "» включён", nil
		}
		return "ключ «" + u.Name + "» отключён", nil
	}, g.actServerCheck)
}

func (g *gui) actServerReset() {
	o, ok := g.serverOwner()
	if !ok {
		return
	}
	u, ok := g.selectedServerUser()
	if !ok {
		return
	}
	if !askYesNo(g.hwnd, "Сбросить трафик",
		"Обнулить счётчик трафика ключа «"+u.Name+"»?\nСейчас: "+quotaText(u.Used, u.Limit)) {
		return
	}
	g.runAsync("Сбрасываю трафик «"+u.Name+"»", func() (string, error) {
		if err := adminResetTraffic(o, u.Name); err != nil {
			return "", err
		}
		return "трафик ключа «" + u.Name + "» обнулён", nil
	}, g.actServerCheck)
}

func (g *gui) actServerDelete() {
	o, ok := g.serverOwner()
	if !ok {
		return
	}
	u, ok := g.selectedServerUser()
	if !ok {
		return
	}
	if !askYesNo(g.hwnd, "Удалить ключ",
		"Удалить ключ «"+u.Name+"» с сервера? Он перестанет работать сразу и безвозвратно.") {
		return
	}
	g.runAsync("Удаляю ключ «"+u.Name+"»", func() (string, error) {
		if err := adminDelete(o, u.Name); err != nil {
			return "", err
		}
		return "ключ «" + u.Name + "» удалён с сервера", nil
	}, g.actServerCheck)
}

// ---------------------------------------------------------------------------
// Страница «Настройки»
// ---------------------------------------------------------------------------

func (g *gui) currentSettings() AppSettings {
	g.app.mu.Lock()
	defer g.app.mu.Unlock()
	return g.app.settings
}

func (g *gui) saveSettings(s AppSettings) {
	if _, err := g.app.settingsSave(s); err != nil {
		alert(g.hwnd, "Усатый VPN", err.Error(), true)
		g.refreshSettings()
		return
	}
	g.log("настройки сохранены: режим «%s»", modeLabel(s.Mode))
	g.refreshSettings()
	g.refresh()
}

func (g *gui) saveMode() {
	s := g.currentSettings()
	switch {
	case checked(g.ctlOf(idModeGame)):
		s.Mode = ModeGame
	case checked(g.ctlOf(idModeAnon)):
		s.Mode = ModeAnon
	default:
		s.Mode = ModeNormal
	}
	g.saveSettings(s)
}

func (g *gui) saveAdvanced() {
	s := g.currentSettings()
	s.Advanced = checked(g.ctlOf(idAdvCheck))
	if !s.Advanced {
		s.Protocol = ProtoAuto
	} else if s.Protocol == ProtoAuto {
		s.Protocol = string(TMTP1)
	}
	g.saveSettings(s)
	g.refreshSettings()
}

func (g *gui) saveProtocol() {
	sel := listBoxSel(g.ctlOf(idProtoList))
	if sel < 0 || sel >= len(g.protoIDs) {
		return
	}
	s := g.currentSettings()
	s.Protocol = g.protoIDs[sel]
	g.saveSettings(s)
}

// ---------------------------------------------------------------------------
// Обновление окна
// ---------------------------------------------------------------------------

// tick вызывается раз в секунду: состояние, журнал фоновой операции, трафик.
func (g *gui) tick() {
	g.pollJob()
	g.refresh()
}

func (g *gui) pollJob() {
	job := g.app.Job()
	if job.Kind == "" {
		return
	}
	if len(job.Lines) < g.jobShown { // началась новая операция
		g.jobShown = 0
	}
	for ; g.jobShown < len(job.Lines); g.jobShown++ {
		g.log("%s", job.Lines[g.jobShown])
	}
	if job.Done && !job.Running {
		if job.Error != "" {
			alert(g.hwnd, "Операция не удалась", job.Error, true)
		}
		g.jobShown = 0
		g.app.jobMu.Lock()
		g.app.job = jobState{}
		g.app.jobMu.Unlock()
		if job.Kind == "install" && job.Error == "" {
			g.page = pageServer
			setChecked(g.ctlOf(idTabConnect), false)
			setChecked(g.ctlOf(idTabServer), true)
			setChecked(g.ctlOf(idTabSettings), false)
			g.layout()
			g.refreshServerList(true)
		}
		g.refresh()
	}
}

// refresh обновляет все надписи окна по текущему состоянию.
func (g *gui) refresh() {
	if g.hwnd == 0 {
		return
	}
	st := g.app.State()
	selID := g.app.keys.Selected()
	selName := ""
	if sk, ok := g.app.keys.Find(selID); ok {
		selName = sk.DisplayName()
	}
	v := stateTexts(st, selName, choiceOfEffective(g.app.effectiveFor(selID)))

	g.setText(idStatus, v.Title)
	g.setText(idKeyLine, v.Key)
	g.setText(idDetails, v.Details)
	g.setText(idChain, v.Chain)
	g.setText(idNotice, v.Notice)
	g.setText(idTraffic, v.Traffic)
	g.setText(idQuota, v.Quota)

	g.refreshKeys(st)
	g.refreshServer(st)
	g.refreshSettingsNote(st)
	g.setText(idFooter, "Данные и журнал: "+filepath.Join(g.dir, "app.log"))

	// Кнопки подключения.
	busy := g.isBusy()
	working := st.Phase == "connected" || st.Phase == "connecting"
	enableWindow(g.ctlOf(idBtnConnect), !busy && !working && selID != "")
	enableWindow(g.ctlOf(idBtnDisconn), !busy && working)
	setWindowText(g.ctlOf(idBtnConnect), connectBtnText(st.Phase))
	enableWindow(g.ctlOf(idBtnAddKey), !busy)
	enableWindow(g.ctlOf(idBtnKeyOpts), !busy && selID != "")
	enableWindow(g.ctlOf(idBtnKeyDel), !busy && selID != "" && g.app.activeKeyID() != selID)
	enableWindow(g.ctlOf(idBtnCopySS), !busy && selID != "")
	loaded := g.app.AmneziaStatus().Loaded
	enableWindow(g.ctlOf(idBtnConf), !busy && !working)
	enableWindow(g.ctlOf(idBtnConfForget), !busy && loaded)
	enableWindow(g.ctlOf(idBtnAwgSetup), !busy && g.app.Owner() != nil)
	enableWindow(g.ctlOf(idBtnQuit), !busy)
}

func connectBtnText(phase string) string {
	if phase == "connecting" {
		return "Подключаюсь…"
	}
	return "Подключить"
}

// capText — первая буква заглавная («отключаю» → «Отключаю»).
func capText(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func (g *gui) isBusy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.busy
}

// refreshKeys перерисовывает список ключей, только если он изменился.
func (g *gui) refreshKeys(st StateDTO) {
	list := g.app.keys.List()
	active := g.app.activeKeyID()
	sel := g.app.keys.Selected()
	lines := make([]string, 0, len(list))
	ids := make([]string, 0, len(list))
	selIdx := -1
	for i, sk := range list {
		host := ""
		if key, err := ParseKey(sk.Text); err == nil {
			host = key.Host
		}
		lines = append(lines, keyLine(sk, host, sk.ID == sel, sk.ID == active, st.Phase))
		ids = append(ids, sk.ID)
		if sk.ID == sel {
			selIdx = i
		}
	}
	sig := strings.Join(lines, "|") + "#" + strconv.Itoa(selIdx) + "#" + st.Phase
	if sig != g.keySig {
		g.keySig = sig
		g.keyIDs = ids
		listBoxSet(g.ctlOf(idKeysList), lines, selIdx, g.s(7))
		setWindowText(g.ctlOf(idKeysEmpty), keysEmptyText(len(lines)))
	}
}

// refreshServer обновляет страницу сервера.
func (g *gui) refreshServer(st StateDTO) {
	_ = st
	info := g.app.ServerStatus()
	title, details := serverInfoText(info, info.Configured)
	g.setText(idSrvTitle, title)
	g.setText(idSrvDetails, details)
	enableWindow(g.ctlOf(idBtnSrvCheck), info.Configured && !g.isBusy())
	enableWindow(g.ctlOf(idBtnSrvForget), info.Configured && !g.isBusy())
	enableWindow(g.ctlOf(idBtnSrvNew), info.Configured && !g.isBusy())
	enableWindow(g.ctlOf(idBtnSrvShow), info.Configured && !g.isBusy())
	enableWindow(g.ctlOf(idBtnSrvToggle), info.Configured && !g.isBusy() && len(g.srvUsers) > 0)
	enableWindow(g.ctlOf(idBtnSrvReset), info.Configured && !g.isBusy() && len(g.srvUsers) > 0)
	enableWindow(g.ctlOf(idBtnSrvDel), info.Configured && !g.isBusy() && len(g.srvUsers) > 0)
	enableWindow(g.ctlOf(idBtnSrvSetup), !g.isBusy())
	if info.Configured && len(g.srvUsers) == 0 {
		setWindowText(g.ctlOf(idSrvEmpty), "Нажмите «Проверить», чтобы получить список ключей с сервера.")
	} else {
		setWindowText(g.ctlOf(idSrvEmpty), srvEmptyText(len(g.srvUsers)))
	}
}

// refreshServerList перерисовывает список ключей сервера.
func (g *gui) refreshServerList(keepSel bool) {
	sel := -1
	if keepSel {
		sel = listBoxSel(g.ctlOf(idSrvList))
	}
	lines := make([]string, 0, len(g.srvUsers))
	for _, u := range g.srvUsers {
		lines = append(lines, serverUserLine(u))
	}
	sig := strings.Join(lines, "|")
	if sig != g.srvSig {
		g.srvSig = sig
		listBoxSet(g.ctlOf(idSrvList), lines, sel, g.s(7))
		setWindowText(g.ctlOf(idSrvEmpty), srvEmptyText(len(lines)))
	}
}

// buildProtoList заполняет список протоколов на странице настроек.
func (g *gui) buildProtoList() {
	labels := []string{"Авто (по режиму)"}
	g.protoIDs = []string{ProtoAuto}
	for _, t := range []Transport{TMTP1, TMTP2, TMTP3, TWG} {
		labels = append(labels, t.Label())
		g.protoIDs = append(g.protoIDs, string(t))
	}
	listBoxSet(g.ctlOf(idProtoList), labels, 0, g.s(7))
}

// refreshSettings показывает сохранённые настройки на странице «Настройки».
func (g *gui) refreshSettings() {
	s := g.currentSettings()
	setChecked(g.ctlOf(idModeGame), s.Mode == ModeGame)
	setChecked(g.ctlOf(idModeNormal), s.Mode == ModeNormal)
	setChecked(g.ctlOf(idModeAnon), s.Mode == ModeAnon)
	setChecked(g.ctlOf(idAdvCheck), s.Advanced)
	setChecked(g.ctlOf(idAwgAsVpn), s.AmneziaAsVpn)
	setChecked(g.ctlOf(idMTPDirect), s.MTPDirect)
	g.setText(idModeHint, "Сейчас выбран режим «"+modeLabel(s.Mode)+"»: "+modeTable[s.Mode].hint)

	sel := 0
	for i, id := range g.protoIDs {
		if id == s.Protocol {
			sel = i
		}
	}
	if !s.Advanced {
		sel = 0
	}
	listBoxSet(g.ctlOf(idProtoList), protoLabels(), sel, g.s(7))
	enableWindow(g.ctlOf(idProtoList), s.Advanced && !g.isBusy())
	g.setText(idProtoHint, protoHint(s))
	g.refreshSettingsNote(g.app.State())
}

func protoLabels() []string {
	out := []string{"Авто (по режиму)"}
	for _, t := range []Transport{TMTP1, TMTP2, TMTP3, TWG} {
		out = append(out, t.Label())
	}
	return out
}

func protoHint(s AppSettings) string {
	if !s.Advanced {
		return "Список недоступен: протокол выбирает режим. Включите «Выбрать протокол вручную», чтобы задать его самим."
	}
	idx := 0
	for i, t := range []Transport{TMTP1, TMTP2, TMTP3, TWG} {
		if string(t) == s.Protocol {
			idx = i + 1
		}
	}
	if idx == 0 {
		return "Авто: игровой режим берёт UDP (MTP/1), анонимный — маскировку (MTP/3), обычный — как в ключе."
	}
	t := []Transport{TMTP1, TMTP2, TMTP3, TWG}[idx-1]
	return "Выбран протокол вручную: " + t.Label() + " — " + t.Hint() +
		". Подключение не начнётся, если в ключе нет параметров для этого протокола."
}

func (g *gui) refreshSettingsNote(st StateDTO) {
	if st.Phase == "connected" || st.Phase == "connecting" {
		g.setText(idSettingsNote, "VPN сейчас работает. Новый режим и протокол применятся при следующем подключении.")
	} else {
		g.setText(idSettingsNote, "Изменения сохранены и применятся при следующем подключении.")
	}
}

// ---------------------------------------------------------------------------
// Тексты для окон
// ---------------------------------------------------------------------------

func keyInfoText(key *ServerKey, sk SavedKey) string {
	lines := []string{"Сервер: " + key.Host, supportsText(key), keyStatText(sk.Stat)}
	if sk.Alias != "" && key.Name != "" && sk.Alias != key.Name {
		lines = append(lines, "Имя в ключе: "+key.Name)
	}
	return strings.Join(lines, "\n")
}

func transportOptions(key *ServerKey, cur Transport) (labels, hints []string, sel int) {
	sel = -1
	for i, t := range allTransports {
		label := t.Label()
		if !key.Supports(t) {
			label += " — нет в этом ключе"
		}
		if t == cur {
			sel = i
		}
		labels = append(labels, label)
		hints = append(hints, t.Hint())
	}
	if sel < 0 {
		sel = 0
	}
	return labels, hints, sel
}

func routingsForUI() []Routing { return []Routing{RAll, RNotRU, RApps} }

func routingOptions(cur Routing) (labels, hints []string, sel int) {
	sel = -1
	for i, r := range routingsForUI() {
		label := r.Label()
		if r == RApps {
			label += " — в Windows не поддерживается"
		}
		if r == cur {
			sel = i
		}
		labels = append(labels, label)
		hints = append(hints, r.Hint())
	}
	if sel < 0 {
		sel = 0
	}
	return labels, hints, sel
}
