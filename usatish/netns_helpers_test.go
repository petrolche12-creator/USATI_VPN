//go:build linux

package main

import (
	"encoding/binary"
	"math/rand"
	"net"
	"net/http"
	"testing"
	"time"
)

// waitPing — ждёт, пока ping пройдёт (ping должен быть установлен).
func waitPing(host string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if pingOK(host) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// waitHTTP — ждёт, пока URL ответит 200.
func waitHTTP(url string, d time.Duration) bool {
	c := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		resp, err := c.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// waitDNS — ждёт ответа от DNS-сервера через туннель (важно само прохождение пакетов).
func waitDNS(addr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if dnsQueryOK(addr) {
			return true
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// dnsQueryOK — короткий UDP-запрос A-записи; важен сам ответ (даже с ошибкой) с адреса DNS-шлюза.
func dnsQueryOK(addr string) bool {
	c, err := net.DialTimeout("udp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	q := make([]byte, 12+5+1+4+4)
	binary.BigEndian.PutUint16(q[0:], uint16(rand.Intn(65535)))
	q[2] = 0x01 // рекурсия
	binary.BigEndian.PutUint16(q[4:], 1)
	i := 12
	q[i] = 1
	q[i+1] = 'a'
	i += 2
	q[i] = 0
	i++
	binary.BigEndian.PutUint16(q[i:], 1)
	binary.BigEndian.PutUint16(q[i+2:], 1)
	if _, err := c.Write(q[:i+4]); err != nil {
		return false
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	return err == nil && n >= 12 && buf[0] == q[0] && buf[1] == q[1]
}

var _ = testing.Short
