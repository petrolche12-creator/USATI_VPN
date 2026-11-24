package core

// Мелкие помощники, чтобы ядро не зависело от пакета main: строки, JSON, форматирование.
// Экспортируемые обёртки нужны и Java/Kotlin (gomobile не умеет возвращать ошибки из функций
// без обработки — поэтому ошибки приходят как Exception, а данные — простыми типами).

import (
	"encoding/json"
	"fmt"
	"strings"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func splitLines(s string) []string { return strings.Split(s, "\n") }

func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// Logf — форматирование для журнала (на случай, если Kotlin присылает свои строки).
func Logf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Version — версия ядра: видно в журнале, легко отличить сборки.
const coreVersion = "2.0-mobile"
