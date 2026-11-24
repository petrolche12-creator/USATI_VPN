package core

// Тест обмена пакетами между ядром и приложением: кадры, рукопожатие, пакеты, статус, остановка.
// Цепочка подменяется — проверяется сам протокол и то, что служба Android получит ровно то,
// что обещано в CoreProcess.kt.

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeChain — «цепочка» для теста: отдаёт заранее заданные пакеты и считает отправленные.
type fakeChain struct {
	mu       sync.Mutex
	addr     string
	dns      string
	mtu      int
	toSend   [][]byte
	received [][]byte
	status   [][3]int64
	stopped  bool
}

func (f *fakeChain) Addr() string { return f.addr }
func (f *fakeChain) DNS() string  { return f.dns }
func (f *fakeChain) MTU() int     { return f.mtu }
func (f *fakeChain) SendPacket(p []byte) error {
	cp := append([]byte(nil), p...)
	f.mu.Lock()
	f.received = append(f.received, cp)
	f.mu.Unlock()
	return nil
}
func (f *fakeChain) RecvPacket(timeoutMs int, l StatusListener) ([]byte, error) {
	f.mu.Lock()
	var st [][3]int64
	if len(f.status) > 0 {
		st = f.status[:1]
		f.status = f.status[1:]
	}
	var p []byte
	if len(f.toSend) > 0 {
		p = f.toSend[0]
		f.toSend = f.toSend[1:]
	}
	f.mu.Unlock()
	if len(st) > 0 && l != nil {
		l.OnStatus(st[0][0], st[0][1], st[0][2])
	}
	if p != nil {
		return p, nil
	}
	time.Sleep(time.Duration(timeoutMs) * time.Millisecond)
	return nil, nil
}
func (f *fakeChain) Stop()        { f.stopped = true }
func (f *fakeChain) Rx() int64    { return 11 }
func (f *fakeChain) Tx() int64    { return 22 }
func (f *fakeChain) AwgRx() int64 { return 33 }
func (f *fakeChain) AwgTx() int64 { return 44 }

// TestServeFrames: приложение получает параметры, пакеты и статус; отправленные пакеты доходят.
func TestServeFrames(t *testing.T) {
	srv, cli := net.Pipe()
	chain := &fakeChain{
		addr: "10.9.0.7", dns: "10.9.0.1", mtu: 1380,
		toSend: [][]byte{{0x45, 1, 2, 3}, {0x45, 4, 5, 6}},
		status: [][3]int64{{100, 1000, 1900000000}},
	}
	logs := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		Serve(srv, chain, func(f string, a ...any) { logs <- sprintf(f, a...) })
		close(done)
	}()

	// 1. Рукопожатие
	typ, payload, err := readFrame(cli, 1<<17)
	if err != nil || typ != frameHello {
		t.Fatalf("нет приветствия: тип %#x, ошибка %v", typ, err)
	}
	var hello helloInfo
	if err := json.Unmarshal(payload, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Addr != "10.9.0.7" || hello.DNS != "10.9.0.1" || hello.MTU != 1380 || hello.Version == "" {
		t.Fatalf("приветствие не такое: %+v", hello)
	}

	// 2. Отправка пакета приложением
	if err := writeFrame(cli, framePacket, []byte{0x45, 9, 9}); err != nil {
		t.Fatal(err)
	}

	// 3. Приём пакетов, статуса и счётчиков
	var packets, stats int
	var status statusInfo
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (packets < 2 || stats == 0) {
		typ, payload, err := readFrame(cli, 1<<17)
		if err != nil {
			t.Fatal(err)
		}
		switch typ {
		case framePacket:
			packets++
		case frameStats:
			stats++
			var s statsInfo
			if err := json.Unmarshal(payload, &s); err != nil {
				t.Fatal(err)
			}
			if s.Rx != 11 || s.Tx != 22 || s.AwgRx != 33 || s.AwgTx != 44 {
				t.Fatalf("счётчики не те: %+v", s)
			}
		case frameStatus:
			if err := json.Unmarshal(payload, &status); err != nil {
				t.Fatal(err)
			}
		}
	}
	if packets != 2 {
		t.Fatalf("пришло пакетов %d, ожидалось 2", packets)
	}
	if status.Used != 100 || status.Limit != 1000 || status.Expires != 1900000000 {
		t.Fatalf("статус не тот: %+v", status)
	}

	// 4. Отправленный приложением пакет дошёл до цепочки
	deadline = time.Now().Add(3 * time.Second)
	for {
		chain.mu.Lock()
		got := len(chain.received)
		chain.mu.Unlock()
		if got > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	chain.mu.Lock()
	received := chain.received
	chain.mu.Unlock()
	if len(received) != 1 || len(received[0]) != 3 || received[0][2] != 9 {
		t.Fatalf("пакет из приложения не дошёл: %v", received)
	}

	// 5. Стоп закрывает обмен
	_ = writeFrame(cli, frameStop, nil)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Serve не завершился после стопа")
	}
	cli.Close()
}

// TestReadFrameLimits: испорченная длина кадра не роняет сервер, а отвергается.
func TestReadFrameLimits(t *testing.T) {
	srv, cli := net.Pipe()
	go func() {
		// тип + длина 0x7fffffff (слишком много)
		_, _ = cli.Write([]byte{framePacket, 0x7f, 0xff, 0xff, 0xff})
	}()
	if _, _, err := readFrame(srv, 1<<17); err == nil {
		t.Fatal("кадр с огромной длиной должен быть отвергнут")
	}
	srv.Close()
	cli.Close()
}
