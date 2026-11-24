package main

// Оркестратор: состояние подключения, ключи, сервер и фоновые операции. HTTP-слой только вызывает методы App.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sessionInfo — что сейчас поднято.
type sessionInfo struct {
	KeyID     string
	KeyName   string
	Host      string
	Transport Transport
	Routing   Routing
	Mode      Mode
}

// jobState — фоновая операция (установка сервера) и её журнал.
// chainState — где сейчас цепочка подключения (для окна).
type chainState struct {
	Stage string `json:"stage"` // idle | awg | ssh | mtp | up
	Text  string `json:"text"`
}

type jobState struct {
	Kind    string   `json:"kind"`
	Running bool     `json:"running"`
	Done    bool     `json:"done"`
	Error   string   `json:"error"`
	Lines   []string `json:"lines"`
}

// App — всё состояние программы.
type App struct {
	dir      string
	logf     func(string, ...any)
	keys     *KeyStore
	settings AppSettings
	started  time.Time

	mu        sync.Mutex
	phase     string // idle | connecting | connected | error
	message   string
	run       tunnelRun
	awgUp     *awgTunnel
	chain     chainState
	sess      *sessionInfo
	startedAt time.Time
	cancel    bool

	srvMu      sync.Mutex
	owner      *Owner
	srvList    *adminList
	srvErr     string
	srvChecked time.Time

	jobMu sync.Mutex
	job   jobState

	pingMu   sync.Mutex
	lastPing time.Time
	quit     chan struct{}
	quitOnce sync.Once
}

// NewApp открывает каталог данных: ключи и сохранённый сервер.
func NewApp(dir string, logf func(string, ...any)) (*App, error) {
	ks, err := OpenKeyStore(filepath.Join(dir, "keys.json"))
	if err != nil {
		return nil, err
	}
	owner, err := LoadOwner(dir)
	if err != nil {
		logf("сохранённые данные сервера не открылись: %v", err)
	}
	set, serr := LoadSettings(dir)
	if serr != nil {
		logf("%v", serr)
	}
	// загруженный ранее .conf AmneziaWG — часть цепочки «Amnezia → SSH → MTP»
	if cfg, aerr := LoadAmnezia(dir); aerr != nil {
		logf("сохранённый .conf Amnezia не открылся: %v", aerr)
	} else if cfg != nil {
		logf("загружен сохранённый .conf Amnezia «%s»: %s", cfg.DisplayName(), cfg.Peer.Endpoint)
	}
	return &App{
		dir:      dir,
		logf:     logf,
		keys:     ks,
		started:  time.Now(),
		phase:    "idle",
		message:  "Не подключено",
		owner:    owner,
		settings: set,
		quit:     make(chan struct{}),
	}, nil
}

func adapterName(t Transport) string {
	if t == TWG {
		return "UsatyiWG"
	}
	return "UsatyiVPN"
}

// Платформенные крючки приватности (на других платформах равны nil): пока поднят туннель,
// Windows перекрывает утечки DNS/IPv6 и в анонимном режиме синхронизирует часовой пояс.
var (
	platformTunUp   func(logf func(string, ...any), dns net.IP, mode Mode, host string)
	platformTunDown func(logf func(string, ...any))
	platformRecover func(dir string, logf func(string, ...any))
)

// Connect запускает подключение выбранного ключа. Работа идёт в фоне: состояние читается через State().
func (a *App) Connect(id string) error {
	sk, ok := a.keys.Find(id)
	if !ok {
		return errors.New("ключ не найден")
	}
	key, err := ParseKey(sk.Text)
	if err != nil {
		return err
	}
	a.mu.Lock()
	set := a.settings
	a.mu.Unlock()
	ch, err := EffectiveChoice(set, sk, key)
	if err != nil {
		return err
	}
	sk.Transport, sk.Routing = ch.Transport, ch.Routing // только для этого подключения: сам ключ не меняется
	if !key.Supports(sk.Transport) {
		return fmt.Errorf("в ключе нет параметров для «%s». Выберите другой способ в настройках ключа", sk.Transport.Label())
	}
	switch {
	case sk.Transport == TSS:
		return errors.New("Shadowsocks работает во внешнем приложении: скопируйте ссылку ss:// и вставьте её в Shadowsocks")
	case sk.Routing == RApps:
		return errors.New("режим «Только выбранные приложения» в Windows не поддерживается: выберите «Весь трафик» или «Кроме российских IP»")
	}
	a.mu.Lock()
	if a.run != nil || a.phase == "connecting" {
		a.mu.Unlock()
		return errors.New("уже подключено или подключаюсь")
	}
	si := &sessionInfo{KeyID: sk.ID, KeyName: sk.DisplayName(), Host: key.Host, Transport: sk.Transport, Routing: sk.Routing, Mode: ch.Mode}
	a.phase = "connecting"
	a.message = "Подключаюсь к " + key.Host + "…"
	a.sess = si
	a.cancel = false
	a.startedAt = time.Now()
	a.mu.Unlock()
	a.logf("подключение: %s, %s, %s, режим: %s", si.KeyName, sk.Transport.Label(), sk.Routing.Label(), modeLabel(ch.Mode))
	go a.runTunnel(sk, key, si)
	return nil
}

// setChain — обновить этап цепочки для окна.
func (a *App) setChain(stage, text string) {
	a.mu.Lock()
	a.chain = chainState{Stage: stage, Text: text}
	a.mu.Unlock()
	if a.logf != nil && text != "" {
		a.logf("%s", text)
	}
}

// runTunnel поднимает цепочку: .conf Amnezia → AmneziaWG → SSH → MTP.
// В аварийном режиме (MTPDirect) — MTP напрямую, без Amnezia и SSH.
func (a *App) runTunnel(sk SavedKey, key *ServerKey, si *sessionInfo) {
	opts := runOpts{Key: key, Transport: sk.Transport, Routing: sk.Routing, NotRU: notRUList, Adapter: adapterName(sk.Transport)}
	fail := func(err error) {
		a.logf("ошибка подключения: %v", err)
		a.mu.Lock()
		a.phase = "error"
		a.message = err.Error()
		a.chain = chainState{Stage: "idle", Text: ""}
		a.mu.Unlock()
	}

	var run tunnelRun
	var err error

	a.mu.Lock()
	direct := a.settings.MTPDirect
	routeAll := a.settings.AmneziaAsVpn
	awgCfg := activeAwg()
	owner := a.owner
	if owner != nil {
		cp := *owner
		owner = &cp
	}
	a.mu.Unlock()

	switch {
	case direct:
		a.setChain("mtp", "Прямое подключение к MTP-VPN без Amnezia и SSH (аварийный режим)")
		if sk.Transport == TWG {
			err = errWGUnsupported
		} else {
			run, err = startMTP(opts)
		}
	case awgCfg == nil:
		err = errors.New("файл .conf Amnezia не загружен: нажмите «Загрузить .conf» на вкладке «Подключение»")
	case owner == nil:
		err = errors.New("сервер не настроен: на вкладке «Подключение» укажите IP, пользователя, пароль SSH и порт")
	default:
		a.mu.Lock()
		a.chain = chainState{Stage: "awg", Text: ""}
		a.mu.Unlock()
		var c *chainRun
		c, err = startChain(chainOpts{
			Cfg:      awgCfg,
			Server:   owner,
			Key:      key,
			Run:      opts,
			RouteAll: routeAll,
			Logf:     a.logf,
			OnHostKey: func(fp string) {
				// запоминаем ключ сервера при первом входе
				a.srvMu.Lock()
				if a.owner != nil && a.owner.HostFP == "" {
					a.owner.HostFP = fp
					_ = SaveOwner(a.dir, a.owner)
				}
				a.srvMu.Unlock()
			},
		})
		if err == nil {
			run = c
		}
	}
	if err != nil {
		fail(err)
		return
	}
	a.mu.Lock()
	if a.cancel {
		a.mu.Unlock()
		run.Stop()
		_ = run.Wait()
		a.mu.Lock()
		a.phase = "idle"
		a.message = "Отключено"
		a.sess = nil
		a.chain = chainState{Stage: "idle", Text: ""}
		a.mu.Unlock()
		return
	}
	a.run = run
	a.phase = "connected"
	a.message = "Подключено"
	a.startedAt = time.Now()
	a.chain = chainState{Stage: "up", Text: ""}
	a.mu.Unlock()
	a.logf("подключено: %s", si.Host)

	if platformTunUp != nil {
		dnsIP := net.IPv4(1, 1, 1, 1)
		if key.Wg != nil {
			if ip := net.ParseIP(key.Wg.DNS).To4(); ip != nil {
				dnsIP = ip
			}
		}
		// В режиме «Amnezia как VPN» маршруты и DNS уже настроены AmneziaWG-туннелем.
		if !routeAll || direct {
			platformTunUp(a.logf, dnsIP, si.Mode, si.Host)
		}
	}

	werr := run.Wait()
	a.mu.Lock()
	a.run = nil
	a.chain = chainState{Stage: "idle", Text: ""}
	if werr != nil {
		a.phase = "error"
		a.message = werr.Error()
		a.logf("туннель остановлен с ошибкой: %v", werr)
	} else {
		a.phase = "idle"
		a.message = "Отключено"
		a.sess = nil
		a.logf("отключено")
	}
	a.mu.Unlock()
	if platformTunDown != nil && (!routeAll || direct) {
		platformTunDown(a.logf)
	}
}

// Disconnect опускает туннель или отменяет подключение в процессе.
func (a *App) Disconnect() {
	a.mu.Lock()
	a.cancel = true
	run := a.run
	if a.phase == "connecting" {
		a.message = "Отменяю подключение…"
	}
	a.mu.Unlock()
	if run != nil {
		run.Stop()
	}
}

// Shutdown — при выходе: опускаем туннель и ждём, пока маршруты уберутся.
func (a *App) Shutdown() {
	a.Disconnect()
	for i := 0; i < 50; i++ {
		a.mu.Lock()
		busy := a.run != nil || a.phase == "connecting"
		a.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// StateDTO — состояние для интерфейса.
type StateDTO struct {
	Phase            string      `json:"phase"`
	Message          string      `json:"message"`
	Session          *SessionDTO `json:"session"`
	Chain            ChainDTO    `json:"chain"`
	StartedAt        int64       `json:"startedAt"`
	Rx               int64       `json:"rx"`
	Tx               int64       `json:"tx"`
	Used             *int64      `json:"used"`
	Limit            *int64      `json:"limit"`
	Expires          *int64      `json:"expires"`
	Selected         string      `json:"selected"`
	ServerConfigured bool        `json:"serverConfigured"`
	NowMs            int64       `json:"nowMs"`
}

// ChainDTO — этап цепочки подключения для карточки статуса.
type ChainDTO struct {
	Stage   string `json:"stage"`
	Text    string `json:"text"`
	Amnezia string `json:"amnezia"` // «загружен» / пусто
	HasSSH  bool   `json:"hasSsh"`
	Direct  bool   `json:"direct"`
}

// SessionDTO — сведения о поднятом сеансе.
type SessionDTO struct {
	KeyID          string `json:"keyId"`
	KeyName        string `json:"keyName"`
	Host           string `json:"host"`
	Transport      string `json:"transport"`
	TransportLabel string `json:"transportLabel"`
	Routing        string `json:"routing"`
	RoutingLabel   string `json:"routingLabel"`
	Mode           string `json:"mode"`
	ModeLabel      string `json:"modeLabel"`
}

// State собирает состояние для интерфейса.
func (a *App) State() StateDTO {
	a.mu.Lock()
	st := StateDTO{Phase: a.phase, Message: a.message, Selected: a.keys.Selected(), NowMs: time.Now().UnixMilli()}
	st.Chain = ChainDTO{Stage: a.chain.Stage, Text: a.chain.Text, HasSSH: a.owner != nil, Direct: a.settings.MTPDirect}
	if c := activeAwg(); c != nil {
		st.Chain.Amnezia = c.DisplayName()
	}
	if a.sess != nil {
		si := a.sess
		st.Session = &SessionDTO{
			KeyID: si.KeyID, KeyName: si.KeyName, Host: si.Host,
			Transport: string(si.Transport), TransportLabel: si.Transport.Label(),
			Routing: string(si.Routing), RoutingLabel: si.Routing.Label(),
			Mode: string(si.Mode), ModeLabel: modeLabel(si.Mode),
		}
	}
	if a.phase == "connected" {
		st.StartedAt = a.startedAt.UnixMilli()
	}
	run := a.run
	sessKey := ""
	if a.sess != nil {
		sessKey = a.sess.KeyID
	}
	a.mu.Unlock()
	if run != nil {
		st.Rx, st.Tx = run.Stats()
		if s, ok := run.Status(); ok {
			used, limit, exp := s.Used, s.Limit, s.Expires
			st.Used, st.Limit, st.Expires = &used, &limit, &exp
			if sessKey != "" {
				if err := a.keys.SetStat(sessKey, s); err != nil {
					a.logf("данные ключа не сохранились: %v", err)
				}
			}
		}
	}
	a.srvMu.Lock()
	st.ServerConfigured = a.owner != nil
	a.srvMu.Unlock()
	return st
}

// Owner возвращает сохранённый сервер (или nil).
func (a *App) Owner() *Owner {
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	if a.owner == nil {
		return nil
	}
	o := *a.owner
	return &o
}

// SetOwner сохраняет сервер (пароль защищается средствами системы).
func (a *App) SetOwner(o *Owner) error {
	if err := SaveOwner(a.dir, o); err != nil {
		return err
	}
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	a.owner = o
	a.srvList = nil
	a.srvErr = ""
	return nil
}

// ForgetOwner стирает сервер с этого компьютера (сам сервер не меняется).
func (a *App) ForgetOwner() error {
	if err := ForgetOwner(a.dir); err != nil {
		return err
	}
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	a.owner = nil
	a.srvList = nil
	a.srvErr = ""
	return nil
}

// ServerKeys запрашивает список ключей на сервере (синхронно, с таймаутами SSH).
func (a *App) ServerKeys() (*adminList, error) {
	o := a.Owner()
	if o == nil {
		return nil, errors.New("сервер не настроен")
	}
	l, err := adminListUsers(o)
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	if err != nil {
		a.srvErr = err.Error()
		return nil, err
	}
	a.srvList = l
	a.srvErr = ""
	a.srvChecked = time.Now()
	return l, nil
}

// ServerInfo — сведения о сервере для интерфейса.
type ServerInfo struct {
	Configured bool   `json:"configured"`
	Host       string `json:"host"`
	SSHUser    string `json:"sshUser"`
	SSHPort    int    `json:"sshPort"`
	VPNPort    int    `json:"vpnPort"`
	Error      string `json:"error"`
	Checked    int64  `json:"checked"`
	Users      int    `json:"users"`
	Online     int    `json:"online"`
}

// ServerStatus — сведения о сервере из последней проверки.
func (a *App) ServerStatus() ServerInfo {
	a.srvMu.Lock()
	defer a.srvMu.Unlock()
	if a.owner == nil {
		return ServerInfo{}
	}
	info := ServerInfo{
		Configured: true, Host: a.owner.Host, SSHUser: a.owner.SSHUser,
		SSHPort: a.owner.SSHPort, VPNPort: a.owner.VPNPort, Error: a.srvErr,
	}
	if !a.srvChecked.IsZero() {
		info.Checked = a.srvChecked.UnixMilli()
	}
	if a.srvList != nil {
		info.Users = len(a.srvList.Users)
		for _, u := range a.srvList.Users {
			if u.Online {
				info.Online++
			}
		}
	}
	return info
}

// startJob запускает фоновую операцию, если другая ещё не идёт.
func (a *App) startJob(kind string, fn func(logf func(string)) error) error {
	a.jobMu.Lock()
	if a.job.Running {
		a.jobMu.Unlock()
		return errors.New("уже выполняется другая операция, дождитесь её окончания")
	}
	a.job = jobState{Kind: kind, Running: true, Lines: []string{}}
	a.jobMu.Unlock()
	go func() {
		err := fn(func(line string) {
			a.jobMu.Lock()
			a.job.Lines = append(a.job.Lines, line)
			a.jobMu.Unlock()
		})
		a.jobMu.Lock()
		defer a.jobMu.Unlock()
		a.job.Running = false
		a.job.Done = true
		if err != nil {
			a.job.Error = err.Error()
			a.job.Lines = append(a.job.Lines, "✖ "+err.Error())
		} else {
			a.job.Lines = append(a.job.Lines, "✔ готово")
		}
	}()
	return nil
}

// Job — журнал последней фоновой операции.
func (a *App) Job() jobState {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	st := a.job
	st.Lines = append([]string{}, a.job.Lines...)
	return st
}

// InstallServer запускает установку сервера по SSH в фоне.
func (a *App) InstallServer(form Owner) error {
	mtpPy, err := readAsset("assets/mtp.py")
	if err != nil {
		return err
	}
	installSh, err := readAsset("assets/install.sh")
	if err != nil {
		return err
	}
	return a.startJob("install", func(logf func(string)) error {
		logf(fmt.Sprintf("Установка: VPN UDP %d, вход %s@%s", form.VPNPort, form.SSHUser, form.Host))
		o, err := installServer(form, mtpPy, installSh, logf)
		if err != nil {
			return err
		}
		if err := a.SetOwner(o); err != nil {
			return err
		}
		logf("сервер сохранён на этом компьютере")
		if err := a.addOwnerKey(o, logf, adminAddKey); err != nil {
			logf("ключ владельца не создан: " + err.Error() + ". Создайте его на вкладке «Создать».")
		}
		return nil
	})
}

// Opened — отзывался ли интерфейс хотя бы раз.
func (a *App) Opened() bool {
	a.pingMu.Lock()
	defer a.pingMu.Unlock()
	return !a.lastPing.IsZero()
}

// Ping отмечает, что окно интерфейса ещё открыто.
func (a *App) Ping() {
	a.pingMu.Lock()
	a.lastPing = time.Now()
	a.pingMu.Unlock()
}

// Quit просит программу закрыться (кнопка «Выйти» или закрытие окна).
func (a *App) Quit() {
	a.quitOnce.Do(func() { close(a.quit) })
}

// closedAfter — сколько ждать пинга, прежде чем считать окно закрытым. Браузер в фоне урезает таймеры
// (в фоновой вкладке пинги могут приходить раз в минуту), поэтому порог больше минуты.
const closedAfter = 150 * time.Second

// Wait блокирует до выхода: по кнопке, либо когда окно закрыто и не отвечает.
func (a *App) Wait() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.quit:
			return
		case <-t.C:
			a.pingMu.Lock()
			last := a.lastPing
			a.pingMu.Unlock()
			if last.IsZero() {
				if time.Since(a.started) > 90*time.Second {
					a.logf("окно не открылось, выхожу")
					return
				}
				continue
			}
			if time.Since(last) > closedAfter {
				a.logf("окно закрыто, выхожу")
				return
			}
		}
	}
}

// defaultDataDir — каталог данных: %APPDATA%\UsatyiVPN.
func defaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "UsatyiVPN")
}
