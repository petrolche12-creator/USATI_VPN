package main

// Общие для всех платформ вещи запуска: журнал в файл и сообщение о критической ошибке.

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// newLogger пишет журнал в app.log рядом с данными (окна консоли у программы нет).
func newLogger(dir string) func(string, ...any) {
	var mu sync.Mutex
	f, _ := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	return func(format string, args ...any) {
		line := time.Now().Format("2006-01-02 15:04:05") + " " + fmt.Sprintf(format, args...) + "\n"
		mu.Lock()
		defer mu.Unlock()
		if f != nil {
			_, _ = f.WriteString(line)
		}
		fmt.Print(line)
	}
}

// fatal сообщает о критической ошибке окном (у программы нет консоли), пишет в журнал и завершает программу.
func fatal(dir string, err error) {
	logf := newLogger(dir)
	logf("ошибка: %v", err)
	showMessage("Усатый VPN "+appVersion+" не запустился",
		fmt.Sprintf("%v\n\nПодробности в журнале: %s", err, filepath.Join(dir, "app.log")), true)
	consolePause()
	os.Exit(1)
}
