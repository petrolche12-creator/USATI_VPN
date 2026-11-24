//go:build windows

package main

// Усатый VPN для Windows: одна программа (.exe) с обычным окном Windows.
//
// Никакого локального сервера и никакого браузера: программа не слушает порты вообще.
// Интерфейс нарисован средствами Win32 (кнопки, списки, поля ввода), а действия вызывают
// методы App напрямую в этом же процессе. Раньше окно открывалось вкладкой браузера на
// 127.0.0.1 — из-за этого у пользователей с повышенными правами появлялся пустой экран.
// Теперь окна браузера нет.
//
// Окно открывается без запроса прав (манифест asInvoker), чтобы программа запускалась
// всегда и везде. Права администратора запрашиваются в момент подключения: программа
// перезапускает себя через «runas» с флагом -connect, и новая копия сразу подключается.

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	// Все окна живут в одном потоке: это требование Win32.
	runtime.LockOSThread()

	dirFlag := flag.String("dir", "", "каталог данных (по умолчанию %APPDATA%\\UsatyiVPN)")
	connectFlag := flag.String("connect", "", "служебный: подключиться этим ключом сразу после старта")
	flag.Parse()

	dir := *dirFlag
	if dir == "" {
		dir = defaultDataDir()
	}

	// Окна консоли у программы нет, поэтому любая внутренняя ошибка показывается окном.
	defer func() {
		if r := recover(); r != nil {
			showMessage("Усатый VPN остановился",
				fmt.Sprintf("Внутренняя ошибка: %v\n\nЖурнал: %s", r, filepath.Join(dir, "app.log")), true)
			consolePause()
			os.Exit(2)
		}
	}()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		fatal(dir, err)
	}
	logf := newLogger(dir)
	logf("запуск: Усатый VPN %s, нативное окно Windows, локальный сервер не используется", appVersion)
	platformTunUp = func(logf func(string, ...any), dns net.IP, mode Mode, host string) {
		tunUpPrivacy(dir, logf, dns, mode, host)
	}
	platformTunDown = tunDownPrivacy
	platformRecover = recoverPrivacy
	if platformRecover != nil {
		platformRecover(dir, logf)
	}
	if isAdmin() {
		logf("права: администратор")
	} else {
		logf("права: обычные — для подключения Windows спросит подтверждение")
	}

	app, err := NewApp(dir, logf)
	if err != nil {
		fatal(dir, err)
	}

	logf("этап: создание окна")
	g, err := newGUI(app, dir, logf)
	if err != nil {
		fatal(dir, err)
	}

	g.log("окно открыто")
	if n := len(app.keys.List()); n == 0 {
		if app.Owner() == nil {
			g.log("ключей пока нет: добавьте присланный ключ («Добавить ключ») или настройте свой сервер на вкладке «Сервер»")
		} else {
			g.log("ключей пока нет: создайте ключ на вкладке «Сервер» или добавьте присланный")
		}
	} else {
		g.log("ключей в списке: %d. Выберите ключ и нажмите «Подключить»", n)
	}
	app.Ping()

	// Повышенная копия, запущенная через «runas -connect …», сразу подключается.
	if *connectFlag != "" {
		g.log("подключаю ключ сразу после запуска (повышенные права)")
		g.connectKey(*connectFlag)
	}

	logf("этап: цикл сообщений окна")
	g.Run()

	app.Shutdown()
	logf("выход")
}
