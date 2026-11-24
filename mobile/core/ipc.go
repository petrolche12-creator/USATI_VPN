package core

// Обмен пакетами между службой Android и ядром.
//
// Ядро (AmneziaWG → SSH → MTP) живёт в отдельном процессе; системный VpnService в Kotlin
// только перекладывает IP-пакеты. Общение — по локальному (абстрактному unix) сокету:
// ни портов, ни разрешений, ни лишнего сетевого кода.
//
// Кадр: [1 байт тип][4 байта длина, старший байт первый][данные].

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

const (
	frameHello  = 0x10 // ядро → приложение: адрес, DNS, MTU (строка JSON)
	frameLog    = 0x11 // ядро → приложение: строка журнала
	frameError  = 0x12 // ядро → приложение: ошибка, соединение закрывается
	framePacket = 0x20 // в обе стороны: один IP-пакет
	frameStatus = 0x22 // ядро → приложение: данные сервера (used/limit/expires)
	frameStats  = 0x24 // ядро → приложение: счётчики трафика
	frameStop   = 0x30 // приложение → ядро: остановиться
)

// packetChain — то, что нужно обмену пакетами от цепочки (в тестах подменяется).
type packetChain interface {
	Addr() string
	DNS() string
	MTU() int
	SendPacket(p []byte) error
	RecvPacket(timeoutMs int, l StatusListener) ([]byte, error)
	Stop()
	Rx() int64
	Tx() int64
	AwgRx() int64
	AwgTx() int64
}

// helloInfo — первое, что получает приложение: без этого нельзя поднять VpnService.
type helloInfo struct {
	Addr    string `json:"addr"`
	DNS     string `json:"dns"`
	MTU     int    `json:"mtu"`
	Version string `json:"version"`
}

type statusInfo struct {
	Used    int64 `json:"used"`
	Limit   int64 `json:"limit"`
	Expires int64 `json:"expires"`
}

type statsInfo struct {
	Rx    int64 `json:"rx"`
	Tx    int64 `json:"tx"`
	AwgRx int64 `json:"awgRx"`
	AwgTx int64 `json:"awgTx"`
}

// frameConn — обмен кадрами поверх одного соединения (ядро ↔ приложение).
type frameConn struct {
	c  net.Conn
	mu sync.Mutex
}

func writeFrame(c net.Conn, typ byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := c.Write(buf)
	return err
}

func readFrame(c net.Conn, max int) (byte, []byte, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(c, head); err != nil {
		return 0, nil, err
	}
	n := int(binary.BigEndian.Uint32(head[1:5]))
	if n < 0 || n > max {
		return 0, nil, errors.New("слишком большой кадр от приложения")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(c, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

// write отправляет кадр; повторные ошибки не страшны — соединение просто закроется.
func (k *frameConn) write(typ byte, payload []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return writeFrame(k.c, typ, payload)
}

func (k *frameConn) writeJSON(typ byte, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = k.write(typ, data)
}

// Serve обслуживает одно подключение приложения, пока оно не закроется.
// Возвращает, когда связь закончилась; цепочку не останавливает — это делает вызывающий.
func Serve(c net.Conn, chain packetChain, logf func(format string, args ...any)) {
	k := &frameConn{c: c}
	defer c.Close()

	sum := helloInfo{Addr: chain.Addr(), DNS: chain.DNS(), MTU: chain.MTU(), Version: coreVersion}
	k.writeJSON(frameHello, sum)
	logf("приложение подключилось к ядру: адрес %s, DNS %s, MTU %d", sum.Addr, sum.DNS, sum.MTU)

	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }

	// Приём пакетов от приложения.
	go func() {
		defer stop()
		for {
			typ, payload, err := readFrame(c, 1<<17)
			if err != nil {
				return
			}
			switch typ {
			case framePacket:
				if err := chain.SendPacket(payload); err != nil {
					k.write(frameError, []byte("отправка пакета не удалась: "+err.Error()))
					return
				}
			case frameStop:
				logf("приложение попросило остановиться")
				return
			}
		}
	}()

	// Отдача пакетов приложению и служебных сообщений.
	go func() {
		defer stop()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			p, err := chain.RecvPacket(300, statusTo(func(used, limit, expires int64) {
				k.writeJSON(frameStatus, statusInfo{Used: used, Limit: limit, Expires: expires})
			}))
			if err != nil {
				k.write(frameError, []byte(err.Error()))
				return
			}
			if p != nil {
				if err := k.write(framePacket, p); err != nil {
					return
				}
			}
			select {
			case <-done:
				return
			case <-tick.C:
				k.writeJSON(frameStats, statsInfo{Rx: chain.Rx(), Tx: chain.Tx(), AwgRx: chain.AwgRx(), AwgTx: chain.AwgTx()})
			default:
			}
		}
	}()

	<-done
	logf("связь с приложением закончилась")
}

// statusTo адаптирует функцию к интерфейсу StatusListener.
type statusTo func(used, limit, expires int64)

func (s statusTo) OnStatus(used, limit, expires int64) { s(used, limit, expires) }
