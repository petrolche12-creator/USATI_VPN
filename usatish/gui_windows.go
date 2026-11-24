//go:build windows

package main

// Нативное окно Windows на Win32 API (user32/gdi32) — БЕЗ локального HTTP-сервера и БЕЗ браузера.
// Программа не слушает порты вообще: интерфейс собран из обычных элементов Windows
// (кнопки, списки, поля ввода), а действия сразу вызывают методы App в этом же процессе.
//
// Всё, что связано с окнами, живёт в одном потоке (runtime.LockOSThread в main_windows.go).
// Долгие операции (SSH, установка сервера) идут в фоне и сообщают результат через PostMessage,
// чтобы окно не зависало.

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// Константы Win32
// ---------------------------------------------------------------------------

const (
	wsOverlapped   = 0x00000000
	wsPopup        = 0x80000000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
	wsClipSiblings = 0x04000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsThickFrame   = 0x00040000
	wsMinimizeBox  = 0x00020000
	wsMaximizeBox  = 0x00010000
	wsGroup        = 0x00020000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	wsVScroll      = 0x00200000
	wsHScroll      = 0x00100000

	bsPushButton    = 0x00000000
	bsDefPushButton = 0x00000001
	bsAutoCheckBox  = 0x00000003
	bsAutoRadio     = 0x00000009
	bsPushLike      = 0x00001000

	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esAutoHScroll = 0x0080
	esReadOnly    = 0x0800
	esWantReturn  = 0x1000

	lbsNotify      = 0x0001
	lbsHasStrings  = 0x0040
	lbsNoIntegralH = 0x0100

	ssLeft       = 0x0000
	ssNoWordWrap = 0x000C

	csVRedraw = 0x0001
	csHRedraw = 0x0002
	csDblClks = 0x0008

	wsExClientEdge = 0x00000200
	wsExAppWindow  = 0x00040000

	wmDestroy         = 0x0002
	wmSize            = 0x0005
	wmSetText         = 0x000C
	wmGetText         = 0x000D
	wmGetTextLength   = 0x000E
	wmClose           = 0x0010
	wmSetFont         = 0x0030
	wmGetMinMaxInfo   = 0x0024
	wmCommand         = 0x0111
	wmTimer           = 0x0113
	wmNCCreate        = 0x0081
	wmEraseBkgnd      = 0x0014
	wmCtlColorStatic  = 0x0138
	wmCtlColorEdit    = 0x0133
	wmCtlColorListBox = 0x0134
	wmDpiChanged      = 0x02E0

	wmAppAsync = 0x8001 // своё: фоновая операция завершилась
	wmAppQuit  = 0x8002 // своё: закрыть программу
	wmAppLog   = 0x8003 // своё: в журнале появились строки из фонового потока

	swHide = 0
	swShow = 5

	bnClicked    = 0
	lnSelChange  = 1
	cbnSelChange = 1

	lbResetContent   = 0x0184
	lbAddString      = 0x0180
	lbGetCurSel      = 0x0188
	lbSetCurSel      = 0x0186
	lbSetHorizExtent = 0x0194

	bmSetCheck = 0x00F1
	bmGetCheck = 0x00F0

	emSetSel      = 0x00B1
	emScrollCaret = 0x00B7

	bstChecked = 1

	idcArrow       = 32512
	idiApplication = 32512
	colorBtnFace   = 15
	colorWindow    = 5

	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	gmemMoveable  = 0x0002
	cfUnicodeText = 13

	spiGetWorkArea = 0x0030

	mbYesNo       = 0x00000004
	mbIconWarning = 0x00000030
	idYes         = 6

	timerState      = 1
	timerStateEvery = 1000

	logMaxLines = 400
)

// ---------------------------------------------------------------------------
// Загрузка функций Win32
// ---------------------------------------------------------------------------

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGDI32    = windows.NewLazySystemDLL("gdi32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx  = modUser32.NewProc("RegisterClassExW")
	pCreateWindowEx   = modUser32.NewProc("CreateWindowExW")
	pDefWindowProc    = modUser32.NewProc("DefWindowProcW")
	pShowWindow       = modUser32.NewProc("ShowWindow")
	pUpdateWindow     = modUser32.NewProc("UpdateWindow")
	pDestroyWindow    = modUser32.NewProc("DestroyWindow")
	pPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
	pGetMessage       = modUser32.NewProc("GetMessageW")
	pTranslateMessage = modUser32.NewProc("TranslateMessage")
	pDispatchMessage  = modUser32.NewProc("DispatchMessageW")
	pSendMessage      = modUser32.NewProc("SendMessageW")
	pPostMessage      = modUser32.NewProc("PostMessageW")
	pSetWindowText    = modUser32.NewProc("SetWindowTextW")
	pEnableWindow     = modUser32.NewProc("EnableWindow")
	pSetTimer         = modUser32.NewProc("SetTimer")
	pKillTimer        = modUser32.NewProc("KillTimer")
	pLoadCursor       = modUser32.NewProc("LoadCursorW")
	pLoadIcon         = modUser32.NewProc("LoadIconW")
	pGetClientRect    = modUser32.NewProc("GetClientRect")
	pGetWindowRect    = modUser32.NewProc("GetWindowRect")
	pMoveWindow       = modUser32.NewProc("MoveWindow")
	pSetWindowPos     = modUser32.NewProc("SetWindowPos")
	pSetFocus         = modUser32.NewProc("SetFocus")
	pGetDpiForWindow  = modUser32.NewProc("GetDpiForWindow")
	pGetDC            = modUser32.NewProc("GetDC")
	pReleaseDC        = modUser32.NewProc("ReleaseDC")
	pSysParamsInfo    = modUser32.NewProc("SystemParametersInfoW")
	pSetForeground    = modUser32.NewProc("SetForegroundWindow")
	pGetSysColorBr    = modUser32.NewProc("GetSysColorBrush")
	pFillRect         = modUser32.NewProc("FillRect")
	pAdjustWindowRect = modUser32.NewProc("AdjustWindowRectEx")
	pOpenClipboard    = modUser32.NewProc("OpenClipboard")
	pCloseClipboard   = modUser32.NewProc("CloseClipboard")
	pEmptyClipboard   = modUser32.NewProc("EmptyClipboard")
	pSetClipboardData = modUser32.NewProc("SetClipboardData")

	pCreateFont    = modGDI32.NewProc("CreateFontW")
	pDeleteObject  = modGDI32.NewProc("DeleteObject")
	pGetDeviceCaps = modGDI32.NewProc("GetDeviceCaps")
	pSetBkMode     = modGDI32.NewProc("SetBkMode")

	pGetModuleHandle = modKernel32.NewProc("GetModuleHandleW")
	pMoveMemory      = modKernel32.NewProc("RtlMoveMemory")
	pGlobalAlloc     = modKernel32.NewProc("GlobalAlloc")
	pGlobalLock      = modKernel32.NewProc("GlobalLock")
	pGlobalUnlock    = modKernel32.NewProc("GlobalUnlock")
)

// gwlpUserData — индекс данных окна (GWLP_USERDATA = -21). Отрицательное число в uintptr
// константой не записать, поэтому берём побитовое дополнение.
const gwlpUserData = ^uintptr(20)

// get/setWindowLongPtr — LongPtrW на 64 битах, LongW на 32. Читать данные окна нужно
// именно Get-вариантом: Set-вариант ещё и записывает значение, и затёр бы их нулём.
var pGetWindowLongPtr = func() *windows.LazyProc {
	if p := modUser32.NewProc("GetWindowLongPtrW"); p.Find() == nil {
		return p
	}
	return modUser32.NewProc("GetWindowLongW")
}()

// setWindowLongPtr — SetWindowLongPtrW на 64 битах, SetWindowLongW на 32.
var pSetWindowLongPtr = func() *windows.LazyProc {
	if p := modUser32.NewProc("SetWindowLongPtrW"); p.Find() == nil {
		return p
	}
	return modUser32.NewProc("SetWindowLongW")
}()

// ---------------------------------------------------------------------------
// Структуры Win32
// ---------------------------------------------------------------------------

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) width() int  { return int(r.Right - r.Left) }
func (r rect) height() int { return int(r.Bottom - r.Top) }

type msgStruct struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type createStruct struct {
	CreateParams uintptr
	Instance     uintptr
	Menu         uintptr
	Parent       uintptr
	Cy           int32
	Cx           int32
	Y            int32
	X            int32
	Style        int32
	Name         uintptr
	Class        uintptr
	ExStyle      uint32
}

type minMaxInfo struct {
	PtReserve     point
	PtMaxSize     point
	PtMaxPosition point
	PtMinTrack    point
	PtMaxTrack    point
}

// ---------------------------------------------------------------------------
// Мелкие помощники Win32
// ---------------------------------------------------------------------------

// u16 — строка в UTF-16 для Win32. Ноль внутри строки заменяется пробелом, иначе Win32 обрежет текст.
func u16(s string) *uint16 {
	s = strings.ReplaceAll(s, "\x00", " ")
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		e, _ := windows.UTF16PtrFromString(" ")
		return e
	}
	return p
}

func send(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := pSendMessage.Call(hwnd, uintptr(m), wp, lp)
	return r
}

func sendStr(hwnd uintptr, m uint32, s string) uintptr {
	return send(hwnd, m, 0, uintptr(unsafe.Pointer(u16(s))))
}

func post(hwnd uintptr, m uint32) {
	pPostMessage.Call(hwnd, uintptr(m), 0, 0)
}

func createWindowEx(exStyle uint32, class, title string, style uint32, x, y, w, h int32,
	parent uintptr, id uintptr, inst uintptr, param uintptr) uintptr {
	r, _, _ := pCreateWindowEx.Call(uintptr(exStyle), uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(title))), uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, inst, param)
	return r
}

func showWindow(hwnd uintptr, show bool) {
	cmd := uintptr(swHide)
	if show {
		cmd = swShow
	}
	pShowWindow.Call(hwnd, cmd)
}

func enableWindow(hwnd uintptr, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	pEnableWindow.Call(hwnd, v)
}

func moveWindow(hwnd uintptr, x, y, w, h int) {
	if hwnd == 0 {
		return
	}
	pMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(w), uintptr(h))
}

func windowText(hwnd uintptr) string {
	n := int(send(hwnd, wmGetTextLength, 0, 0))
	if n <= 0 {
		return ""
	}
	buf := make([]uint16, n+2)
	send(hwnd, wmGetText, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	return windows.UTF16ToString(buf)
}

func setWindowText(hwnd uintptr, s string) {
	if hwnd == 0 {
		return
	}
	pSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(u16(s))))
}

func clientRect(hwnd uintptr) rect {
	var r rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func windowRect(hwnd uintptr) rect {
	var r rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func checked(hwnd uintptr) bool { return send(hwnd, bmGetCheck, 0, 0) == bstChecked }

func setChecked(hwnd uintptr, on bool) {
	v := uintptr(0)
	if on {
		v = bstChecked
	}
	send(hwnd, bmSetCheck, v, 0)
}

// listBoxSet заполняет список заново и восстанавливает выбор.
func listBoxSet(hwnd uintptr, items []string, sel, charW int) {
	send(hwnd, lbResetContent, 0, 0)
	maxLen := 0
	for _, it := range items {
		sendStr(hwnd, lbAddString, it)
		if n := len([]rune(it)); n > maxLen {
			maxLen = n
		}
	}
	if sel >= 0 && sel < len(items) {
		send(hwnd, lbSetCurSel, uintptr(sel), 0)
	}
	if charW < 4 {
		charW = 7
	}
	send(hwnd, lbSetHorizExtent, uintptr(maxLen*charW+charW*4), 0)
}

func listBoxSel(hwnd uintptr) int {
	r := int32(send(hwnd, lbGetCurSel, 0, 0))
	if r < 0 {
		return -1
	}
	return int(r)
}

// askYesNo — вопрос с кнопками «Да» и «Нет».
func askYesNo(owner uintptr, title, text string) bool {
	t, x := u16(title), u16(text)
	r, _ := windows.MessageBox(windows.HWND(owner), x, t, mbYesNo|mbIconWarning|mbSetForeground)
	return r == idYes
}

// alert — окно с сообщением.
func alert(owner uintptr, title, text string, isError bool) {
	icon := uint32(mbIconInformation)
	if isError {
		icon = mbIconError
	}
	t, x := u16(title), u16(text)
	_, _ = windows.MessageBox(windows.HWND(owner), x, t, icon|mbSetForeground|mbTopmost)
}

// copyText кладёт текст в буфер обмена Windows.
func copyText(hwnd uintptr, s string) bool {
	u16s, err := windows.UTF16FromString(s)
	if err != nil {
		return false
	}
	n := len(u16s) * 2
	if r, _, _ := pOpenClipboard.Call(hwnd); r == 0 {
		return false
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(n))
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return false
	}
	// Копируем текст в блок памяти Windows. Приёмник — адрес из GlobalLock (uintptr),
	// поэтому копируем через RtlMoveMemory, а не через unsafe-указатель.
	src := make([]byte, n)
	for i, c := range u16s {
		src[2*i] = byte(c)
		src[2*i+1] = byte(c >> 8)
	}
	pMoveMemory.Call(p, uintptr(unsafe.Pointer(&src[0])), uintptr(n))
	pGlobalUnlock.Call(h)
	r, _, _ := pSetClipboardData.Call(cfUnicodeText, h)
	return r != 0
}

// ---------------------------------------------------------------------------
// Реестр окон: одно окно — один объект Go
// ---------------------------------------------------------------------------

type wndObject interface {
	onMessage(hwnd uintptr, m uint32, wp, lp uintptr) uintptr
}

var (
	wndRegMu sync.Mutex
	wndReg   = map[int64]wndObject{}
	wndSeq   int64
)

func wndRegister(o wndObject) int64 {
	wndRegMu.Lock()
	defer wndRegMu.Unlock()
	wndSeq++
	wndReg[wndSeq] = o
	return wndSeq
}

func wndLookup(id int64) wndObject {
	wndRegMu.Lock()
	defer wndRegMu.Unlock()
	return wndReg[id]
}

func wndRelease(id int64) {
	wndRegMu.Lock()
	defer wndRegMu.Unlock()
	delete(wndReg, id)
}

func userData(hwnd uintptr) uintptr {
	r, _, _ := pGetWindowLongPtr.Call(hwnd, uintptr(gwlpUserData))
	return r
}

// wndProc — общая процедура окна: находит объект Go по данным окна и отдаёт сообщение ему.
var wndProcCB = windows.NewCallback(func(hwnd, m, wp, lp uintptr) uintptr {
	msg := uint32(m)
	if msg == wmNCCreate {
		cs := (*createStruct)(unsafe.Pointer(lp))
		if cs != nil && cs.CreateParams != 0 {
			pSetWindowLongPtr.Call(hwnd, uintptr(gwlpUserData), cs.CreateParams)
		}
	}
	id := userData(hwnd)
	if id == 0 {
		return defProc(hwnd, msg, wp, lp)
	}
	obj := wndLookup(int64(id))
	if obj == nil {
		return defProc(hwnd, msg, wp, lp)
	}
	return obj.onMessage(hwnd, msg, wp, lp)
})

func defProc(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(m), wp, lp)
	return r
}

// loadAppIcon — иконка из ресурсов программы (id 1), иначе 0.
func loadAppIcon(inst uintptr) uintptr {
	r, _, _ := pLoadIcon.Call(inst, 1)
	return r
}

// registerClass регистрирует класс окна (повторная регистрация не мешает).
func registerClass(name string, inst uintptr) {
	var wc wndClassEx
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	wc.Style = csHRedraw | csVRedraw | csDblClks
	wc.LpfnWndProc = wndProcCB
	wc.HInstance = inst
	br, _, _ := pGetSysColorBr.Call(colorBtnFace)
	wc.HbrBackground = br
	wc.HCursor, _, _ = pLoadCursor.Call(0, idcArrow)
	if icon := loadAppIcon(inst); icon != 0 {
		wc.HIcon = icon
		wc.HIconSm = icon
	} else {
		wc.HIcon, _, _ = pLoadIcon.Call(0, idiApplication)
		wc.HIconSm = wc.HIcon
	}
	wc.LpszClassName = u16(name)
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
}

// ---------------------------------------------------------------------------
// Шрифты и масштаб экрана
// ---------------------------------------------------------------------------

const (
	fontNormal = 0
	fontBold   = 1
	fontBig    = 2
	fontMono   = 3
)

type fontSpec struct {
	face   string
	points int
	bold   bool
}

var fontSpecs = [4]fontSpec{
	{"Segoe UI", 10, false},
	{"Segoe UI", 10, true},
	{"Segoe UI Semibold", 14, true},
	{"Consolas", 9, false},
}

func createFonts(dpi int) [4]uintptr {
	var out [4]uintptr
	for i, sp := range fontSpecs {
		weight := uintptr(400)
		if sp.bold {
			weight = 700
		}
		height := -int32(sp.points * dpi / 72)
		r, _, _ := pCreateFont.Call(uintptr(uint32(height)), 0, 0, 0, weight, 0, 0, 0,
			1, // DEFAULT_CHARSET
			0, 0,
			5, // CLEARTYPE_QUALITY
			0, uintptr(unsafe.Pointer(u16(sp.face))))
		out[i] = r
	}
	return out
}

// hasGetDpiForWindow: GetDpiForWindow есть только в Windows 10 1607+; на старых
// версиях вызов LazyProc паникует, поэтому наличие функции проверяем один раз.
var hasGetDpiForWindow = pGetDpiForWindow.Find() == nil

func windowDPI(hwnd uintptr) int {
	if hwnd != 0 && hasGetDpiForWindow {
		if r, _, _ := pGetDpiForWindow.Call(hwnd); r != 0 {
			return int(r)
		}
	}
	dc, _, _ := pGetDC.Call(0)
	if dc != 0 {
		r, _, _ := pGetDeviceCaps.Call(dc, 90) // LOGPIXELSY
		pReleaseDC.Call(0, dc)
		if r != 0 {
			return int(r)
		}
	}
	return 96
}

// eraseBackground заливает клиентскую область окна цветом окна программы.
func eraseBackground(hwnd, hdc uintptr) {
	var r rect
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	br, _, _ := pGetSysColorBr.Call(colorBtnFace)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), br)
}

// windowSizeFor — размер окна с рамкой под нужную клиентскую область.
func windowSizeFor(style, exStyle uint32, cw, ch int) (int, int) {
	r := rect{Right: int32(cw), Bottom: int32(ch)}
	pAdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), uintptr(style), 0, uintptr(exStyle))
	return r.width(), r.height()
}

// workArea — рабочая область экрана (без панели задач).
func workArea() (int, int) {
	var r rect
	ok, _, _ := pSysParamsInfo.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0)
	if ok == 0 || r.width() <= 0 {
		return 1280, 800
	}
	return r.width(), r.height()
}

// ---------------------------------------------------------------------------
// Главное окно
// ---------------------------------------------------------------------------

const (
	mainClass = "UsatyiVPNMainWindow"

	pageConnect  = 0
	pageServer   = 1
	pageSettings = 2
)

// Идентификаторы элементов окна.
const (
	idTabConnect  = 1
	idTabServer   = 2
	idTabSettings = 3

	idStatus     = 10
	idKeyLine    = 11
	idDetails    = 12
	idNotice     = 13
	idTraffic    = 14
	idQuota      = 15
	idBtnConnect = 16
	idBtnDisconn = 17
	idKeysHead   = 18
	idKeysList   = 19
	idKeysEmpty  = 20
	idBtnAddKey  = 21
	idBtnKeyOpts = 22
	idBtnKeyDel  = 23
	idBtnCopySS  = 24

	idChain         = 25 // строка цепочки AmneziaWG → SSH → MTP
	idBtnConf       = 26 // «Загрузить .conf Amnezia…»
	idBtnConfForget = 27 // «Убрать .conf»
	idBtnAwgSetup   = 28 // «Поставить AmneziaWG на сервер»

	idSrvTitle     = 30
	idSrvDetails   = 31
	idBtnSrvSetup  = 32
	idBtnSrvCheck  = 33
	idBtnSrvForget = 34
	idSrvKeysHead  = 35
	idSrvList      = 36
	idSrvEmpty     = 37
	idBtnSrvNew    = 38
	idBtnSrvShow   = 39
	idBtnSrvToggle = 40
	idBtnSrvReset  = 41
	idBtnSrvDel    = 42

	idModeHead      = 50
	idModeGame      = 51
	idModeNormal    = 52
	idModeAnon      = 53
	idModeHint      = 54
	idAdvCheck      = 55
	idAdvHint       = 56
	idProtoList     = 57
	idProtoHint     = 58
	idSettingsNote  = 59
	idAwgAsVpn      = 60
	idAwgAsVpnHint  = 61
	idMTPDirect     = 62
	idMTPDirectHint = 63

	idLogHead = 70
	idLog     = 71
	idFooter  = 72
	idBtnQuit = 73
)

// gui — главное окно программы.
type gui struct {
	app  *App
	dir  string
	logf func(string, ...any)

	hwnd  uintptr
	inst  uintptr
	dpi   int
	fonts [4]uintptr

	page int
	ctl  map[int]uintptr
	text map[int]string // последние показанные тексты: не перерисовывать без нужды

	keyIDs   []string
	keySig   string
	srvUsers []adminUser
	srvSig   string

	protoIDs []string

	mu         sync.Mutex
	logLines   []string
	logPending string
	layoutSig  string
	pending    []asyncResult
	busy       bool
	busyLabel  string
	modal      bool
	jobShown   int
	quitting   bool
}

// s — размер в пикселях при текущем масштабе экрана.
func (g *gui) s(v int) int { return v * g.dpi / 96 }

func (g *gui) ctlOf(id int) uintptr { return g.ctl[id] }

// setText обновляет подпись, только если она изменилась (иначе окно мерцает раз в секунду).
func (g *gui) setText(id int, s string) {
	h := g.ctlOf(id)
	if h == 0 || g.text[id] == s {
		return
	}
	g.text[id] = s
	setWindowText(h, s)
}

// newGUI создаёт окно и все элементы. Ошибка возвращается, если окно не создалось.
func newGUI(app *App, dir string, logf func(string, ...any)) (*gui, error) {
	inst, _, _ := pGetModuleHandle.Call(0)
	g := &gui{app: app, dir: dir, logf: logf, inst: inst,
		ctl: map[int]uintptr{}, text: map[int]string{}}
	registerClass(mainClass, inst)

	g.dpi = windowDPI(0)
	ww, hh := workArea()
	w, h := g.s(780), g.s(680)
	if w > ww-g.s(16) {
		w = ww - g.s(16)
	}
	if h > hh-g.s(16) {
		h = hh - g.s(16)
	}
	x, y := (ww-w)/2, (hh-h)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	id := wndRegister(g)
	hwnd := createWindowEx(wsExAppWindow, mainClass, "Усатый VPN "+appVersion,
		wsOverlapped|wsCaption|wsSysMenu|wsThickFrame|wsMinimizeBox|wsMaximizeBox|wsClipChildren|wsClipSiblings,
		int32(x), int32(y), int32(w), int32(h), 0, 0, inst, uintptr(id))
	if hwnd == 0 {
		wndRelease(id)
		return nil, fmt.Errorf("окно программы не создалось: %v", windows.GetLastError())
	}
	g.hwnd = hwnd
	g.dpi = windowDPI(hwnd)
	g.fonts = createFonts(g.dpi)

	g.createControls()
	g.buildProtoList()
	g.refreshSettings()
	g.layout()
	g.refresh()

	pSetTimer.Call(hwnd, timerState, timerStateEvery, 0)
	pShowWindow.Call(hwnd, swShow)
	pUpdateWindow.Call(hwnd)
	pSetForeground.Call(hwnd)
	return g, nil
}

// createControls создаёт все элементы окна. Что видно — настраивает layout().
func (g *gui) createControls() {
	child := func(class, title string, style uint32, id int) uintptr {
		h := createWindowEx(0, class, title, wsChild|style, 0, 0, 10, 10, g.hwnd, uintptr(id), g.inst, 0)
		g.ctl[id] = h
		send(h, wmSetFont, g.fonts[fontNormal], 1)
		return h
	}
	btn := func(title string, style uint32, id int) uintptr {
		return child("BUTTON", title, bsPushButton|style|wsTabStop, id)
	}

	// Вкладки — обычные кнопки-переключатели, без общих элементов управления.
	btn("Подключение", bsAutoRadio|bsPushLike|wsGroup, idTabConnect)
	btn("Сервер", bsAutoRadio|bsPushLike, idTabServer)
	btn("Настройки", bsAutoRadio|bsPushLike, idTabSettings)
	setChecked(g.ctlOf(idTabConnect), true)

	// Страница «Подключение».
	child("STATIC", "", ssLeft, idStatus)
	send(g.ctlOf(idStatus), wmSetFont, g.fonts[fontBig], 1)
	child("STATIC", "", ssLeft, idKeyLine)
	send(g.ctlOf(idKeyLine), wmSetFont, g.fonts[fontBold], 1)
	child("STATIC", "", ssLeft, idDetails)
	child("STATIC", "", ssLeft, idChain)
	child("STATIC", "", ssLeft, idNotice)
	child("STATIC", "", ssNoWordWrap, idTraffic)
	send(g.ctlOf(idTraffic), wmSetFont, g.fonts[fontMono], 1)
	child("STATIC", "", ssNoWordWrap, idQuota)
	send(g.ctlOf(idQuota), wmSetFont, g.fonts[fontMono], 1)
	child("STATIC", "Мои ключи на этом компьютере. Нажмите ключ, чтобы выбрать его:", ssLeft, idKeysHead)
	child("LISTBOX", "", lbsNotify|lbsHasStrings|lbsNoIntegralH|wsBorder|wsVScroll|wsHScroll|wsTabStop, idKeysList)
	child("STATIC", "Ключей пока нет. Нажмите «Добавить ключ» и вставьте строку mtp://, которую вам прислали.", ssLeft, idKeysEmpty)
	btn("Подключить", 0, idBtnConnect)
	btn("Отключить", 0, idBtnDisconn)
	btn("Добавить ключ…", 0, idBtnAddKey)
	btn("Настройки ключа…", 0, idBtnKeyOpts)
	btn("Удалить ключ", 0, idBtnKeyDel)
	btn("Скопировать ss://", 0, idBtnCopySS)
	btn("Загрузить .conf Amnezia…", 0, idBtnConf)
	btn("Убрать .conf", 0, idBtnConfForget)
	btn("Поставить AmneziaWG на сервер…", 0, idBtnAwgSetup)

	// Страница «Сервер».
	child("STATIC", "", ssLeft, idSrvTitle)
	send(g.ctlOf(idSrvTitle), wmSetFont, g.fonts[fontBig], 1)
	child("STATIC", "", ssLeft, idSrvDetails)
	btn("Настроить сервер…", 0, idBtnSrvSetup)
	btn("Проверить", 0, idBtnSrvCheck)
	btn("Забыть сервер", 0, idBtnSrvForget)
	child("STATIC", "Ключи на сервере. Лимит и срок проверяет сервер: исчерпанный или истёкший ключ перестаёт работать сразу.", ssLeft, idSrvKeysHead)
	child("LISTBOX", "", lbsNotify|lbsHasStrings|lbsNoIntegralH|wsBorder|wsVScroll|wsHScroll|wsTabStop, idSrvList)
	child("STATIC", "Список ещё не загружен. Нажмите «Проверить».", ssLeft, idSrvEmpty)
	btn("Создать ключ…", 0, idBtnSrvNew)
	btn("Показать ключ…", 0, idBtnSrvShow)
	btn("Включить / отключить", 0, idBtnSrvToggle)
	btn("Сбросить трафик", 0, idBtnSrvReset)
	btn("Удалить ключ", 0, idBtnSrvDel)

	// Страница «Настройки».
	child("STATIC", "Режим работы — действует для всех ключей при следующем подключении:", ssLeft, idModeHead)
	btn("Игровой", bsAutoRadio|wsGroup, idModeGame)
	btn("Обычный", bsAutoRadio, idModeNormal)
	btn("Максимально анонимный", bsAutoRadio, idModeAnon)
	child("STATIC", "", ssLeft, idModeHint)
	btn("Выбрать протокол вручную (расширенные настройки)", bsAutoCheckBox, idAdvCheck)
	child("STATIC", "Выключено: протокол выбирает режим. Включено: вы сами выбираете MTP/1, MTP/2, MTP/3 или WireGuard — для всех ключей, где этот способ есть.", ssLeft, idAdvHint)
	child("LISTBOX", "", lbsNotify|lbsHasStrings|lbsNoIntegralH|wsBorder|wsVScroll|wsTabStop, idProtoList)
	child("STATIC", "", ssLeft, idProtoHint)
	btn("Весь трафик компьютера — в туннель AmneziaWG", bsAutoCheckBox, idAwgAsVpn)
	child("STATIC", "Выключено: в туннель идёт только связь с сервером MTP-VPN, остальной трафик идёт как обычно. Включено: весь трафик компьютера уходит через сервер Amnezia (файл .conf).", ssLeft, idAwgAsVpnHint)
	btn("Аварийный режим: MTP-VPN напрямую, без Amnezia и SSH", bsAutoCheckBox, idMTPDirect)
	child("STATIC", "Для случаев, когда .conf Amnezia недоступен, а сервер MTP-VPN открыт наружу. Обычный порядок — сначала AmneziaWG, затем MTP через SSH.", ssLeft, idMTPDirectHint)
	child("STATIC", "", ssLeft, idSettingsNote)

	// Общие: журнал и выход.
	child("STATIC", "Журнал", ssLeft, idLogHead)
	child("EDIT", "", esMultiline|esAutoVScroll|esReadOnly|wsBorder|wsVScroll, idLog)
	send(g.ctlOf(idLog), wmSetFont, g.fonts[fontMono], 1)
	child("STATIC", "", ssLeft, idFooter)
	btn("Выйти", 0, idBtnQuit)
}

// ---------------------------------------------------------------------------
// Размещение элементов
// ---------------------------------------------------------------------------

type layoutCell struct {
	id     int
	weight int
}

type layoutRow struct {
	cells []layoutCell
	h     int // высота; меньше нуля — строка растягивается
	flex  int // доля свободного места
	gap   int // отступ после строки
}

func row1(id, h, gap int) layoutRow {
	return layoutRow{cells: []layoutCell{{id: id, weight: 1}}, h: h, gap: gap}
}

func rowN(h, gap int, ids ...int) layoutRow {
	cells := make([]layoutCell, 0, len(ids))
	for _, id := range ids {
		cells = append(cells, layoutCell{id: id, weight: 1})
	}
	return layoutRow{cells: cells, h: h, gap: gap}
}

func rowFlex(flex, gap int, ids ...int) layoutRow {
	r := rowN(-1, gap, ids...)
	r.flex = flex
	return r
}

// layout размещает элементы текущей страницы. Сверху вкладки, снизу журнал и «Выйти» — они на всех страницах.
func (g *gui) layout() {
	if g.hwnd == 0 {
		return
	}
	cl := clientRect(g.hwnd)
	cw, ch := cl.width(), cl.height()
	if cw < 50 || ch < 50 {
		return
	}
	pad := g.s(12)
	gap := g.s(7)
	bh := g.s(32)
	tabH := g.s(30)
	footerH := g.s(30)
	logHeadH := g.s(18)
	inner := cw - 2*pad

	// Пустые строки (нет ошибки, нет трафика, нет лимита) не занимают место:
	// их высоту отдаём списку ключей.
	noticeH, trafficH, quotaH := 0, 0, 0
	if strings.TrimSpace(g.text[idNotice]) != "" {
		noticeH = g.s(38)
	}
	if strings.TrimSpace(g.text[idTraffic]) != "" {
		trafficH = g.s(18)
	}
	if strings.TrimSpace(g.text[idQuota]) != "" {
		quotaH = g.s(18)
	}
	keysEmptyH := 0
	if len(g.keyIDs) == 0 {
		keysEmptyH = g.s(36)
	}
	srvEmptyH := 0
	if len(g.srvUsers) == 0 {
		srvEmptyH = g.s(36)
	}

	// Вкладки.
	tw := (inner - 2*gap) / 3
	for i, id := range []int{idTabConnect, idTabServer, idTabSettings} {
		moveWindow(g.ctlOf(id), pad+i*(tw+gap), pad, tw, tabH)
	}
	y := pad + tabH + g.s(12)

	var rows []layoutRow
	switch g.page {
	case pageServer:
		rows = []layoutRow{
			row1(idSrvTitle, g.s(26), g.s(4)),
			row1(idSrvDetails, g.s(42), g.s(8)),
			rowN(bh, g.s(10), idBtnSrvSetup, idBtnSrvCheck, idBtnSrvForget),
			row1(idSrvKeysHead, g.s(36), g.s(4)),
			rowFlex(3, g.s(7), idSrvList),
			row1(idSrvEmpty, srvEmptyH, g.s(6)),
			rowN(bh, g.s(7), idBtnSrvNew, idBtnSrvShow),
			rowN(bh, 0, idBtnSrvToggle, idBtnSrvReset, idBtnSrvDel),
		}
	case pageSettings:
		rows = []layoutRow{
			row1(idModeHead, g.s(20), g.s(4)),
			rowN(g.s(28), g.s(4), idModeGame, idModeNormal, idModeAnon),
			row1(idModeHint, g.s(58), g.s(10)),
			row1(idAdvCheck, g.s(24), g.s(2)),
			row1(idAdvHint, g.s(42), g.s(6)),
			{cells: []layoutCell{{id: idProtoList, weight: 45}, {id: idProtoHint, weight: 55}}, h: g.s(96), gap: g.s(8)},
			row1(idAwgAsVpn, g.s(24), g.s(2)),
			row1(idAwgAsVpnHint, g.s(52), g.s(6)),
			row1(idMTPDirect, g.s(24), g.s(2)),
			row1(idMTPDirectHint, g.s(40), g.s(6)),
			row1(idSettingsNote, g.s(36), 0),
		}
	default: // pageConnect
		rows = []layoutRow{
			row1(idStatus, g.s(28), g.s(2)),
			row1(idKeyLine, g.s(20), g.s(6)),
			row1(idDetails, g.s(38), 0),
			row1(idChain, g.s(34), g.s(2)),
			row1(idNotice, noticeH, g.s(4)),
			row1(idTraffic, trafficH, 0),
			row1(idQuota, quotaH, g.s(8)),
			row1(idKeysHead, g.s(20), g.s(4)),
			rowFlex(3, g.s(7), idKeysList),
			row1(idKeysEmpty, keysEmptyH, g.s(6)),
			rowN(bh, g.s(7), idBtnConnect, idBtnDisconn),
			rowN(g.s(30), 0, idBtnAddKey, idBtnKeyOpts, idBtnKeyDel, idBtnCopySS),
			rowN(g.s(30), 0, idBtnConf, idBtnConfForget, idBtnAwgSetup),
		}
	}

	fixed, flexTotal := 0, 0
	for _, r := range rows {
		if r.h >= 0 {
			fixed += r.h
		} else {
			flexTotal += r.flex
		}
		fixed += r.gap
	}
	bottomH := logHeadH + g.s(72) + gap + footerH
	extra := ch - y - pad - bottomH - fixed
	if extra < 0 || flexTotal <= 0 {
		extra = 0
	}

	cy := y
	for _, r := range rows {
		h := r.h
		if h < 0 {
			h = extra * r.flex / flexTotal
			if h < g.s(64) {
				h = g.s(64)
			}
		}
		x, rem := pad, inner
		for i, c := range r.cells {
			w := rem
			if i < len(r.cells)-1 {
				share := 0
				for _, cc := range r.cells[i:] {
					share += cc.weight
				}
				w = rem * c.weight / share
			}
			if w < 20 {
				w = 20
			}
			moveWindow(g.ctlOf(c.id), x, cy, w, h)
			x += w + gap
			rem -= w + gap
		}
		cy += h + r.gap
	}

	// Журнал — всё, что осталось внизу.
	logH := ch - pad - footerH - gap - cy - logHeadH - gap
	if logH < g.s(46) {
		logH = g.s(46)
	}
	moveWindow(g.ctlOf(idLogHead), pad, cy, inner, logHeadH)
	moveWindow(g.ctlOf(idLog), pad, cy+logHeadH+gap, inner, logH)
	moveWindow(g.ctlOf(idFooter), pad, ch-pad-footerH+g.s(6), inner-g.s(124), g.s(20))
	moveWindow(g.ctlOf(idBtnQuit), cw-pad-g.s(116), ch-pad-footerH, g.s(116), footerH)

	g.applyPageVisibility()
}

// applyPageVisibility показывает элементы своей страницы и прячет чужие.
func (g *gui) applyPageVisibility() {
	pages := map[int][]int{
		pageConnect: {
			idStatus, idKeyLine, idDetails, idChain, idNotice, idTraffic, idQuota,
			idKeysHead, idKeysList, idKeysEmpty, idBtnConnect, idBtnDisconn,
			idBtnAddKey, idBtnKeyOpts, idBtnKeyDel, idBtnCopySS,
			idBtnConf, idBtnConfForget, idBtnAwgSetup,
		},
		pageServer: {
			idSrvTitle, idSrvDetails, idBtnSrvSetup, idBtnSrvCheck, idBtnSrvForget,
			idSrvKeysHead, idSrvList, idSrvEmpty, idBtnSrvNew, idBtnSrvShow,
			idBtnSrvToggle, idBtnSrvReset, idBtnSrvDel,
		},
		pageSettings: {
			idModeHead, idModeGame, idModeNormal, idModeAnon, idModeHint,
			idAdvCheck, idAdvHint, idProtoList, idProtoHint, idSettingsNote,
			idAwgAsVpn, idAwgAsVpnHint, idMTPDirect, idMTPDirectHint,
		},
	}
	for p, ids := range pages {
		for _, id := range ids {
			showWindow(g.ctlOf(id), p == g.page)
		}
	}
	for _, id := range []int{idTabConnect, idTabServer, idTabSettings, idLogHead, idLog, idFooter, idBtnQuit} {
		showWindow(g.ctlOf(id), true)
	}
	// Подсказки «ключей нет» нужны, только пока список пуст, пустые строки скрыты.
	setWindowText(g.ctlOf(idKeysEmpty), keysEmptyText(len(g.keyIDs)))
	setWindowText(g.ctlOf(idSrvEmpty), srvEmptyText(len(g.srvUsers)))
	showWindow(g.ctlOf(idNotice), strings.TrimSpace(g.text[idNotice]) != "")
	showWindow(g.ctlOf(idTraffic), strings.TrimSpace(g.text[idTraffic]) != "")
	showWindow(g.ctlOf(idQuota), strings.TrimSpace(g.text[idQuota]) != "")
	showWindow(g.ctlOf(idKeysEmpty), len(g.keyIDs) == 0 && g.page == pageConnect)
	showWindow(g.ctlOf(idSrvEmpty), len(g.srvUsers) == 0 && g.page == pageServer)
}

func keysEmptyText(n int) string {
	if n > 0 {
		return ""
	}
	return "Ключей пока нет. Нажмите «Добавить ключ» и вставьте строку mtp://, которую вам прислали."
}

func srvEmptyText(n int) string {
	if n > 0 {
		return ""
	}
	return "Список ещё не загружен. Нажмите «Проверить» — программа спросит ключи у сервера."
}

// ---------------------------------------------------------------------------
// Цикл сообщений
// ---------------------------------------------------------------------------

// Run крутит цикл сообщений Windows, пока окно не закрыто.
func (g *gui) Run() {
	var m msgStruct
	for {
		r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		code := int32(r)
		if code == 0 || code == -1 {
			return
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (g *gui) onMessage(hwnd uintptr, m uint32, wp, lp uintptr) uintptr {
	switch m {
	case wmSize:
		g.layout()
	case wmDpiChanged:
		if d := int(uint32(wp & 0xFFFF)); d > 0 {
			g.dpi = d
		}
		for _, f := range g.fonts {
			if f != 0 {
				pDeleteObject.Call(f)
			}
		}
		g.fonts = createFonts(g.dpi)
		g.reapplyFonts()
		r := (*rect)(unsafe.Pointer(lp))
		pSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.width()), uintptr(r.height()),
			swpNoZOrder|swpNoActivate)
		g.layout()
	case wmGetMinMaxInfo:
		mi := (*minMaxInfo)(unsafe.Pointer(lp))
		mi.PtMinTrack = point{X: int32(g.s(620)), Y: int32(g.s(520))}
	case wmEraseBkgnd:
		eraseBackground(hwnd, wp)
		return 1
	case wmCtlColorStatic:
		pSetBkMode.Call(wp, 1) // TRANSPARENT
		br, _, _ := pGetSysColorBr.Call(colorBtnFace)
		return br
	case wmCtlColorEdit, wmCtlColorListBox:
		br, _, _ := pGetSysColorBr.Call(colorWindow)
		return br
	case wmTimer:
		if wp == timerState {
			g.tick()
		}
	case wmCommand:
		g.onCommand(wp, lp)
	case wmAppAsync:
		g.asyncDone()
	case wmAppLog:
		g.mu.Lock()
		text := g.logPending
		g.logPending = ""
		g.mu.Unlock()
		if text != "" {
			g.setText(idLog, text)
			h := g.ctlOf(idLog)
			send(h, emSetSel, ^uintptr(0), ^uintptr(0))
			send(h, emScrollCaret, 0, 0)
		}
	case wmAppQuit:
		g.quit(true)
		return 0
	case wmClose:
		g.quit(true)
		return 0
	case wmDestroy:
		pKillTimer.Call(hwnd, timerState)
		g.mu.Lock()
		g.quitting = true
		g.mu.Unlock()
		pPostQuitMessage.Call(0)
		return 0
	}
	return defProc(hwnd, m, wp, lp)
}

func (g *gui) reapplyFonts() {
	kinds := map[int]int{
		idStatus: fontBig, idSrvTitle: fontBig, idKeyLine: fontBold, idChain: fontBold,
		idTraffic: fontMono, idQuota: fontMono, idLog: fontMono,
	}
	for id, h := range g.ctl {
		k, ok := kinds[id]
		if !ok {
			k = fontNormal
		}
		send(h, wmSetFont, g.fonts[k], 1)
	}
}

// ---------------------------------------------------------------------------
// Журнал окна
// ---------------------------------------------------------------------------

// log пишет строку в журнал окна и в файл app.log.
func (g *gui) log(format string, args ...any) {
	line := format
	if len(args) > 0 {
		line = fmt.Sprintf(format, args...)
	}
	g.logf("%s", line)
	g.mu.Lock()
	g.logLines = append(g.logLines, time.Now().Format("15:04:05")+"  "+line)
	if len(g.logLines) > logMaxLines {
		g.logLines = g.logLines[len(g.logLines)-logMaxLines:]
	}
	g.mu.Unlock()
	g.flushLog()
}

// flushLog передаёт текст журнала в поток окна: из фоновых потоков трогать элементы нельзя.
func (g *gui) flushLog() {
	g.mu.Lock()
	g.logPending = strings.Join(g.logLines, "\r\n")
	g.mu.Unlock()
	post(g.hwnd, wmAppLog)
}
