// Команда usatyi-core — отдельный процесс ядра для Android.
//
// Зачем отдельный процесс:
//   - у него свой сетевой стек AmneziaWG, и он исключён из системного VPN, поэтому пакеты
//     туннеля не заворачиваются в наш же VPN (никаких protect() из Go не нужно);
//   - Kotlin-служба остаётся тонкой: приняла пакет из VpnService — отправила по сокету.
//
// Запуск (из Kotlin, через ProcessBuilder / libc):
//
//	usatyi-core -socket /data/user/0/ru.usatyi.vpn/cache/core.sock -params /data/.../chain.json
//
// -socket — путь к unix-сокету (или @имя для абстрактного), -params — файл с JSON
// параметрами подключения (тот же формат, что у core.StartChain).
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"usatyi.ru/vpn/mobile/core"
)

func main() {
	socket := flag.String("socket", "", "имя абстрактного unix-сокета")
	paramsPath := flag.String("params", "", "файл с параметрами подключения (JSON)")
	confPath := flag.String("conf", "", "файл .conf Amnezia (если не в params)")
	flag.Parse()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "core: "+format+"\n", args...)
	}

	if *socket == "" || *paramsPath == "" {
		logf("нужны -socket и -params")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*paramsPath)
	if err != nil {
		logf("не прочитал параметры: %v", err)
		os.Exit(1)
	}
	// Файл с параметрами не должен оставаться на диске: в нём пароль SSH.
	_ = os.Remove(*paramsPath)
	if *confPath != "" {
		_ = os.Remove(*confPath)
	}

	chain, err := safeStart(string(raw), logf)
	if err != nil {
		logf("подключение не удалось: %v", err)
		os.Exit(1)
	}
	defer safeStop(chain)

	addr := *socket
	if addr == "" {
		logf("пустое имя сокета")
		os.Exit(2)
	}
	// Файловый сокет мог остаться от прошлого запуска: приложение запускает нас заново.
	if addr[0] != '@' {
		_ = os.Remove(addr)
	}
	ln, err := net.Listen("unix", addr)
	if err != nil {
		logf("не открыл сокет %s: %v", addr, err)
		os.Exit(1)
	}
	defer ln.Close()
	if addr[0] != '@' {
		defer os.Remove(addr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	logf("ядро готово, жду приложение")
	conn, err := ln.Accept()
	if err != nil {
		logf("приложение не подключилось: %v", err)
		return
	}
	// Служба в ядре тоже может упасть на редком пакете или гонке в библиотеке.
	// Тогда приложение получит закрытый сокет и покажет ошибку, а не пустоту.
	func() {
		defer func() {
			if r := recover(); r != nil {
				logf("PANIC в цикле ядра: %v\n%s", r, debug.Stack())
			}
		}()
		core.Serve(conn, chain, logf)
	}()
}

// safeStart поднимает цепочку, превращая панику в обычную ошибку.
// Без этого исключение в Go-ядре убивает процесс, и Android показывает
// только "ядро завершилось при запуске" без причины.
func safeStart(paramsJSON string, logf loggerFunc) (chain *core.Chain, err error) {
	defer func() {
		if r := recover(); r != nil {
			logf("PANIC при запуске ядра: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("ядро упало при запуске: %v", r)
		}
	}()
	return core.StartChain(paramsJSON, logf)
}

// safeStop опускает туннель и не даёт панике при закрытии убить процесс.
func safeStop(chain *core.Chain) {
	defer func() {
		if r := recover(); r != nil {
			// журнала здесь уже может не быть — печатаем в stderr
			fmt.Fprintf(os.Stderr, "core: PANIC при остановке: %v\n%s\n", r, debug.Stack())
		}
	}()
	chain.Stop()
}

// loggerFunc — журнал ядра в stderr (родитель читает его из процесса).
type loggerFunc func(format string, args ...any)

func (l loggerFunc) Log(line string) { l("%s", line) }
