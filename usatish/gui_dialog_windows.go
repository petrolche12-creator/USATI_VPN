//go:build windows

package main

// Окна ввода (формы) для нативного интерфейса: тоже чистый Win32, без браузера и без localhost.
// Форма — отдельное окно, принадлежащее главному: пока оно открыто, главное окно недоступно,
// а сообщения обрабатывает тот же поток (вложенный цикл сообщений).

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	formClass = "UsatyiVPNFormWindow"

	esPassword = 0x0020

	formIDOK     = 1
	formIDCancel = 2
	formIDField  = 100 // + номер поля
	formIDHint   = 400 // + номер поля
	formIDExtra  = 20  // + номер дополнительной кнопки
)

var pIsDialogMessage = modUser32.NewProc("IsDialogMessageW")

// formField — одно поле формы.
type formField struct {
	label     string
	value     string
	password  bool
	multiline bool
	readonly  bool
	options   []string // если заданы — список вместо поля ввода
	optHints  []string // подсказка к каждому пункту списка
	sel       int
}

// formResult — что получилось в форме.
type formResult struct {
	ok     bool
	values []string
	sels   []int
	extra  int // номер нажатой дополнительной кнопки, -1 если её не нажимали
}

// formWnd — окно формы.
type formWnd struct {
	g      *gui
	hint   string
	fields []formField
	res    formResult

	hwnd   uintptr
	ctl    []uintptr
	hints  []uintptr
	id     int64
	closed bool
}

// textHeight — примерная высота многострочной подписи (7 px на знак при масштабе 100%).
func (g *gui) textHeight(text string, widthPx int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	per := widthPx / (7 * g.s(1))
	if per < 12 {
		per = 12
	}
	lines := 0
	for _, para := range strings.Split(text, "\n") {
		n := len([]rune(para))
		l := n / per
		if n%per != 0 || n == 0 {
			l++
		}
		lines += l
	}
	return lines*g.s(17) + g.s(4)
}

// showForm открывает форму и ждёт ответа. ok=false — пользователь отменил.
func (g *gui) showForm(title, hint string, fields []formField, okLabel string, extra ...string) formResult {
	registerClass(formClass, g.inst)
	f := &formWnd{g: g, hint: hint, fields: fields}
	f.res.extra = -1
	f.res.sels = make([]int, len(fields))
	f.res.values = make([]string, len(fields))
	for i, fl := range fields {
		f.res.values[i] = fl.value
		f.res.sels[i] = fl.sel
	}

	pad := g.s(14)
	w := g.s(500)
	for _, fl := range fields {
		if fl.multiline {
			w = g.s(600)
		}
	}
	inner := w - 2*pad

	// Считаем высоту.
	h := pad
	if hint != "" {
		h += g.textHeight(hint, inner) + g.s(8)
	}
	fieldH := make([]int, len(fields))
	for i, fl := range fields {
		hh := g.s(18) + g.s(2) // подпись
		switch {
		case fl.options != nil:
			n := len(fl.options)
			if n < 1 {
				n = 1
			}
			hh += n*g.s(19) + g.s(6)
			if len(fl.optHints) > 0 {
				hh += g.s(36)
			}
		case fl.multiline:
			hh += g.s(96)
		default:
			hh += g.s(26)
		}
		fieldH[i] = hh
		h += hh + g.s(6)
	}
	h += g.s(34) + pad
	clientH := h

	// Высота окна с рамкой под клиентскую область; не больше экрана.
	w, h = windowSizeFor(wsPopup|wsCaption|wsSysMenu|wsClipChildren, 0, w, clientH)
	_, waH := workArea()
	if h > waH-g.s(20) {
		h = waH - g.s(20)
		clientH = h
	}

	f.id = wndRegister(f)
	r := windowRect(g.hwnd)
	x := r.Left + int32((r.width()-w)/2)
	y := r.Top + int32((r.height()-h)/3)
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	hwnd := createWindowEx(0, formClass, title, wsPopup|wsCaption|wsSysMenu|wsClipChildren,
		int32(x), int32(y), int32(w), int32(h), g.hwnd, 0, g.inst, uintptr(f.id))
	if hwnd == 0 {
		wndRelease(f.id)
		alert(g.hwnd, "Усатый VPN", "Не удалось открыть окно ввода: "+windows.GetLastError().Error(), true)
		return formResult{extra: -1}
	}
	f.hwnd = hwnd
	f.ctl = make([]uintptr, len(fields))
	f.hints = make([]uintptr, len(fields))

	child := func(class, text string, style uint32, id int, x, y, w, h int) uintptr {
		hw := createWindowEx(0, class, text, wsChild|wsVisible|style, int32(x), int32(y), int32(w), int32(h),
			hwnd, uintptr(id), g.inst, 0)
		send(hw, wmSetFont, g.fonts[fontNormal], 1)
		return hw
	}

	cy := pad
	if hint != "" {
		hh := g.textHeight(hint, inner)
		child("STATIC", hint, ssLeft, 900, pad, cy, inner, hh)
		cy += hh + g.s(8)
	}
	for i, fl := range fields {
		child("STATIC", fl.label, ssLeft, formIDHint+i+500, pad, cy, inner, g.s(18))
		cy += g.s(20)
		switch {
		case fl.options != nil:
			n := len(fl.options)
			if n < 1 {
				n = 1
			}
			lh := n*g.s(19) + g.s(6)
			lb := child("LISTBOX", "", lbsNotify|lbsHasStrings|lbsNoIntegralH|wsBorder|wsVScroll|wsTabStop,
				formIDField+i, pad, cy, inner, lh)
			listBoxSet(lb, fl.options, fl.sel, g.s(7))
			f.ctl[i] = lb
			cy += lh + g.s(2)
			if len(fl.optHints) > 0 {
				txt := ""
				if fl.sel >= 0 && fl.sel < len(fl.optHints) {
					txt = fl.optHints[fl.sel]
				}
				f.hints[i] = child("STATIC", txt, ssLeft, formIDHint+i, pad, cy, inner, g.s(36))
				cy += g.s(36)
			}
		case fl.multiline:
			var st uint32 = esMultiline | esAutoVScroll | esAutoHScroll | esWantReturn | wsBorder | wsVScroll | wsHScroll | wsTabStop
			if fl.readonly {
				st |= esReadOnly
			}
			if fl.password {
				st |= esPassword
			}
			f.ctl[i] = child("EDIT", fl.value, st, formIDField+i, pad, cy, inner, g.s(96))
			cy += g.s(96)
		default:
			var st uint32 = esAutoHScroll | wsBorder | wsTabStop
			if fl.password {
				st |= esPassword
			}
			if fl.readonly {
				st |= esReadOnly
			}
			f.ctl[i] = child("EDIT", fl.value, st, formIDField+i, pad, cy, inner, g.s(26))
			cy += g.s(26) + g.s(6)
			continue
		}
		cy += g.s(6)
	}

	// Кнопки внизу, справа налево: [дополнительные…] [Отмена] [Готово].
	btnW, btnH := g.s(130), g.s(32)
	btnY := clientH - pad - btnH
	bx := pad + inner - btnW
	child("BUTTON", okLabel, bsPushButton|bsDefPushButton|wsTabStop, formIDOK, bx, btnY, btnW, btnH)
	bx -= btnW + g.s(8)
	child("BUTTON", "Отмена", bsPushButton|wsTabStop, formIDCancel, bx, btnY, btnW, btnH)
	for i, lbl := range extra {
		bx -= btnW + g.s(8)
		if bx < pad {
			break
		}
		child("BUTTON", lbl, bsPushButton|wsTabStop, formIDExtra+i, bx, btnY, btnW, btnH)
	}

	// Фокус на первое поле.
	if len(f.ctl) > 0 && f.ctl[0] != 0 {
		pSetFocus.Call(f.ctl[0])
		if len(fields) > 0 && fields[0].multiline {
			send(f.ctl[0], emSetSel, 0, 0)
		}
	}

	return g.modalLoop(f)
}

// modalLoop показывает форму и крутит вложенный цикл сообщений, пока форма не закроется.
func (g *gui) modalLoop(f *formWnd) formResult {
	g.mu.Lock()
	g.modal = true
	g.mu.Unlock()
	enableWindow(g.hwnd, false)
	pShowWindow.Call(f.hwnd, swShow)
	pUpdateWindow.Call(f.hwnd)
	pSetForeground.Call(f.hwnd)

	var m msgStruct
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		code := int32(r)
		if code == -1 {
			break
		}
		if code == 0 { // программа закрывается
			pPostQuitMessage.Call(0)
			break
		}
		if f.hwnd != 0 {
			if d, _, _ := pIsDialogMessage.Call(f.hwnd, uintptr(unsafe.Pointer(&m))); d != 0 {
				continue
			}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		if f.closed {
			break
		}
	}

	enableWindow(g.hwnd, true)
	pSetForeground.Call(g.hwnd)
	g.mu.Lock()
	g.modal = false
	g.mu.Unlock()
	return f.res
}

// collect собирает значения полей.
func (f *formWnd) collect() {
	for i, fl := range f.fields {
		if f.ctl[i] == 0 {
			continue
		}
		if fl.options != nil {
			sel := listBoxSel(f.ctl[i])
			if sel < 0 {
				sel = 0
			}
			f.res.sels[i] = sel
			if sel < len(fl.options) {
				f.res.values[i] = fl.options[sel]
			}
			continue
		}
		f.res.values[i] = windowText(f.ctl[i])
	}
}

func (f *formWnd) finish(ok bool) {
	if ok {
		f.collect()
	}
	f.res.ok = ok
	f.closed = true
	if f.hwnd != 0 {
		pDestroyWindow.Call(f.hwnd)
		f.hwnd = 0
	}
}

func (f *formWnd) onMessage(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	switch m {
	case wmCommand:
		code := int((wp >> 16) & 0xFFFF)
		cid := int(wp & 0xFFFF)
		switch {
		case cid == formIDOK:
			f.finish(true)
			return 0
		case cid == formIDCancel:
			f.finish(false)
			return 0
		case cid >= formIDExtra && cid < formIDField:
			f.res.extra = cid - formIDExtra
			f.collect()
			f.res.ok = false
			f.closed = true
			if f.hwnd != 0 {
				pDestroyWindow.Call(f.hwnd)
				f.hwnd = 0
			}
			return 0
		case cid >= formIDField && cid < formIDField+len(f.fields) && code == lnSelChange:
			i := cid - formIDField
			if i < len(f.fields) && f.hints[i] != 0 && len(f.fields[i].optHints) > 0 {
				sel := listBoxSel(f.ctl[i])
				if sel >= 0 && sel < len(f.fields[i].optHints) {
					setWindowText(f.hints[i], f.fields[i].optHints[sel])
				}
			}
			return 0
		}
	case wmClose:
		f.finish(false)
		return 0
	case wmEraseBkgnd:
		eraseBackground(hwnd, wp)
		return 1
	case wmCtlColorStatic:
		pSetBkMode.Call(wp, 1)
		br, _, _ := pGetSysColorBr.Call(colorBtnFace)
		return br
	case wmCtlColorEdit, wmCtlColorListBox:
		br, _, _ := pGetSysColorBr.Call(colorWindow)
		return br
	case wmDestroy:
		f.closed = true
		wndRelease(f.id)
		return 0
	}
	return defProc(hwnd, m, wp, lp)
}

// showText — окно с текстом, который можно скопировать (например, созданный ключ).
func (g *gui) showText(title, hint, text string) {
	res := g.showForm(title, hint, []formField{{
		label: "Текст (только чтение):", value: text, multiline: true, readonly: true,
	}}, "Скопировать", "Закрыть")
	if res.ok {
		if copyText(g.hwnd, text) {
			g.log("скопировано в буфер обмена")
		} else {
			alert(g.hwnd, "Усатый VPN", "Не удалось скопировать: буфер обмена занят другой программой", true)
		}
	}
}

// ---------------------------------------------------------------------------
// Фоновые операции
// ---------------------------------------------------------------------------

// asyncResult — результат фоновой операции.
type asyncResult struct {
	text  string
	isErr bool
	after func()
}

// runAsync выполняет долгую операцию в фоне: окно не зависает, результат приходит сообщением.
func (g *gui) runAsync(label string, fn func() (string, error), after func()) {
	g.mu.Lock()
	if g.busy {
		what := g.busyLabel
		g.mu.Unlock()
		alert(g.hwnd, "Усатый VPN", "Сейчас идёт другая операция: "+what+". Дождитесь её окончания.", false)
		return
	}
	g.busy = true
	g.busyLabel = label
	g.mu.Unlock()

	g.setBusy(true)
	g.log("%s…", label)
	go func() {
		text, err := fn()
		res := asyncResult{text: text, after: after}
		if err != nil {
			res.text = err.Error()
			res.isErr = true
		}
		g.mu.Lock()
		g.pending = append(g.pending, res)
		g.mu.Unlock()
		post(g.hwnd, wmAppAsync)
	}()
}

// asyncDone вызывается в потоке окна, когда фоновая операция закончилась.
func (g *gui) asyncDone() {
	g.mu.Lock()
	list := g.pending
	g.pending = nil
	g.busy = false
	g.busyLabel = ""
	g.mu.Unlock()
	g.setBusy(false)
	for _, r := range list {
		if r.isErr {
			g.log("ошибка: %s", r.text)
			alert(g.hwnd, "Усатый VPN", r.text, true)
		} else if r.text != "" {
			g.log("%s", r.text)
		}
		if r.after != nil {
			r.after()
		}
	}
	g.refresh()
}

// setBusy делает кнопки недоступными, пока идёт фоновая операция.
func (g *gui) setBusy(busy bool) {
	off := []int{
		idBtnConnect, idBtnDisconn, idBtnAddKey, idBtnKeyOpts, idBtnKeyDel, idBtnCopySS,
		idBtnSrvSetup, idBtnSrvCheck, idBtnSrvForget, idBtnSrvNew, idBtnSrvShow,
		idBtnSrvToggle, idBtnSrvReset, idBtnSrvDel, idBtnQuit,
		idModeGame, idModeNormal, idModeAnon, idAdvCheck,
	}
	for _, id := range off {
		if h := g.ctlOf(id); h != 0 {
			enableWindow(h, !busy)
		}
	}
}
