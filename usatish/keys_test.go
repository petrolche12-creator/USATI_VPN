package main

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"testing"
)

// sampleKeyText — строка mtp:// для тестов хранилища.
func sampleKeyText(t *testing.T) string {
	t.Helper()
	k := &ServerKey{
		Name: "Вася", Host: "203.0.113.5", UID: bytes.Repeat([]byte{1}, 8), Secret: bytes.Repeat([]byte{2}, 32),
		Ports: map[string]int{"udp": 443, "tcp": 8443, "mask": 443, "wg": 51820, "ss": 20001},
		Wg: &WgParams{
			PrivateKey:      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
			ServerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
			Address:         "10.9.0.2/32", DNS: "1.1.1.1",
		},
		Ss: &SsParams{Method: "chacha20-ietf-poly1305", Password: "пароль-123"},
	}
	return EncodeKey(k)
}

// TestKeyStatSaved: данные сервера по ключу переживают перезапуск программы и не пишутся повторно.
func TestKeyStatSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	ks, err := OpenKeyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	sk, _, _, err := ks.AddOrUpdate(sampleKeyText(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.SetStat(sk.ID, serverStatus{Used: 1234, Limit: 5678, Expires: 1900000000, At: 1700000000000}); err != nil {
		t.Fatal(err)
	}
	back, err := OpenKeyStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := back.Find(sk.ID)
	if !ok || got.Stat == nil {
		t.Fatal("после перезапуска данных ключа нет")
	}
	if got.Stat.Used != 1234 || got.Stat.Limit != 5678 || got.Stat.Expires != 1900000000 || got.Stat.At != 1700000000000 {
		t.Fatalf("данные ключа сохранились не так: %+v", *got.Stat)
	}
	// тот же момент STATUS не должен менять запись
	if err := back.SetStat(sk.ID, serverStatus{Used: 9, Limit: 9, Expires: 9, At: 1700000000000}); err != nil {
		t.Fatal(err)
	}
	again, _ := back.Find(sk.ID)
	if again.Stat.Used != 1234 {
		t.Fatal("повторный STATUS с тем же моментом перезаписал данные")
	}
}

// fakeRun — туннель для проверки: отдаёт заданный STATUS.
type fakeRun struct{ st serverStatus }

func (f fakeRun) Stop()                        {}
func (f fakeRun) Wait() error                  { return nil }
func (f fakeRun) Stats() (rx, tx int64)        { return 10, 20 }
func (f fakeRun) Status() (serverStatus, bool) { return f.st, true }

// TestStateSavesStatus: состояние при подключении сохраняет последний STATUS в ключе.
func TestStateSavesStatus(t *testing.T) {
	ks, err := OpenKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	sk, _, _, err := ks.AddOrUpdate(sampleKeyText(t))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		logf: func(string, ...any) {},
		keys: ks,
		run:  fakeRun{st: serverStatus{Used: 42, Limit: 100, Expires: 0, At: 1700000000000}},
		sess: &sessionInfo{KeyID: sk.ID},
	}
	st := app.State()
	if st.Used == nil || *st.Used != 42 {
		t.Fatal("State не отдал STATUS")
	}
	got, _ := ks.Find(sk.ID)
	if got.Stat == nil || got.Stat.Used != 42 || got.Stat.At != 1700000000000 {
		t.Fatalf("STATUS не сохранился в ключе: %+v", got.Stat)
	}
}
