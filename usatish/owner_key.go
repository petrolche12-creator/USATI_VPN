package main

import (
	"fmt"
	"strings"
)

// ownerKeyName — имя ключа, который программа создаёт сама сразу после установки сервера.
const ownerKeyName = "Владелец"

// addOwnerKey создаёт ключ владельца на только что установленном сервере и сразу добавляет его на этот компьютер.
// Если имя уже занято (например, при переустановке), пробует «Владелец 2», «Владелец 3» и так далее.
// Ошибка возвращается вызывающему: сама установка к этому моменту уже сохранена.
func (a *App) addOwnerKey(o *Owner, logf func(string), add func(*Owner, string, int, float64) (string, error)) error {
	var lastErr error
	for i := 1; i <= 5; i++ {
		name := ownerKeyName
		if i > 1 {
			name = fmt.Sprintf("%s %d", ownerKeyName, i)
		}
		logf("создаю ключ «" + name + "» на сервере…")
		text, err := add(o, name, 0, 0) // без срока и без лимита: это ключ владельца
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "уже") {
				continue
			}
			return err
		}
		sk, _, _, err := a.keys.AddOrUpdate(text)
		if err != nil {
			return err
		}
		if err := a.keys.Select(sk.ID); err != nil {
			return err
		}
		logf("ключ «" + name + "» добавлен на этот компьютер и выбран")
		return nil
	}
	return lastErr
}
