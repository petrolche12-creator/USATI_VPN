package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestOwnerKeyAfterInstall: после установки ключ владельца создаётся, добавляется на компьютер и выбирается.
func TestOwnerKeyAfterInstall(t *testing.T) {
	ks, err := OpenKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{keys: ks}
	var lines []string
	var names []string
	add := func(o *Owner, name string, days int, limit float64) (string, error) {
		names = append(names, name)
		if days != 0 || limit != 0 {
			t.Fatalf("ключ владельца без срока и лимита, получили %d дней и %v ГБ", days, limit)
		}
		return sampleKeyText(t), nil
	}
	if err := a.addOwnerKey(&Owner{}, func(s string) { lines = append(lines, s) }, add); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != ownerKeyName {
		t.Fatalf("имя ключа: %v", names)
	}
	list := ks.List()
	if len(list) != 1 || ks.Selected() != list[0].ID {
		t.Fatalf("ключ не добавлен или не выбран: %+v", list)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "добавлен") {
		t.Fatal("в журнале нет сообщения о добавлении ключа")
	}
}

// TestOwnerKeyNameTaken: если имя занято (переустановка), берётся следующее свободное.
func TestOwnerKeyNameTaken(t *testing.T) {
	ks, err := OpenKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{keys: ks}
	var names []string
	add := func(o *Owner, name string, days int, limit float64) (string, error) {
		names = append(names, name)
		if name == ownerKeyName {
			return "", errors.New("ключ с таким именем уже есть")
		}
		return sampleKeyText(t), nil
	}
	if err := a.addOwnerKey(&Owner{}, func(string) {}, add); err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[1] != ownerKeyName+" 2" {
		t.Fatalf("после занятого имени должно идти «%s 2», получили %v", ownerKeyName, names)
	}
}

// TestOwnerKeyFailure: ошибка создания возвращается, ключей на компьютере не появляется.
func TestOwnerKeyFailure(t *testing.T) {
	ks, err := OpenKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{keys: ks}
	add := func(o *Owner, name string, days int, limit float64) (string, error) {
		return "", errors.New("сервер не отвечает")
	}
	if err := a.addOwnerKey(&Owner{}, func(string) {}, add); err == nil {
		t.Fatal("ошибка создания должна возвращаться")
	}
	if len(ks.List()) != 0 {
		t.Fatal("при ошибке ключ на компьютере появиться не должен")
	}
}
