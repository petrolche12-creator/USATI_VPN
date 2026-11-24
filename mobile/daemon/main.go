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

	chain, err := core.StartChain(string(raw), loggerFunc(logf))
	if err != nil {
		logf("подключение не удалось: %v", err)
		os.Exit(1)
	}
	defer chain.Stop()

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
	core.Serve(conn, chain, logf)
}

// loggerFunc — журнал ядра в stderr (родитель читает его из процесса).
type loggerFunc func(format string, args ...any)

func (l loggerFunc) Log(line string) { l("%s", line) }
