//go:build linux

package main

// Живая проверка ответа на IPv6 на Linux: TUN в отдельном сетевом пространстве, весь IPv6 — в TUN,
// curl -6 к внешнему адресу. Без ответа curl ждёт таймаут; с ответом ICMPv6 — должен сразу получить ошибку.
// Запуск (сетевое пространство создаёт вызывающий):
//   sudo ip netns add usatyi-v6
//   sudo ip netns exec usatyi-v6 env USATYI_ICMP6_LIVE=1 ./usatyi.test -test.run ICMP6Live -test.v
//   sudo ip netns del usatyi-v6

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

func runIP(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ip %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// curl6 — сколько заняла попытка curl -6 и что он написал.
func curl6(t *testing.T) (time.Duration, string) {
	t.Helper()
	start := time.Now()
	out, err := exec.Command("curl", "-6", "-sS", "--max-time", "6", "-o", "/dev/null",
		"http://[2001:db8::1]/").CombinedOutput()
	d := time.Since(start)
	if err == nil {
		return d, "неожиданно: соединение прошло"
	}
	return d, strings.TrimSpace(string(out))
}

// answerer отвечает на каждый IPv6-пакет из TUN ICMPv6 «адрес недоступен» (так работает программа).
func answerer(dev awgtun.Device) {
	bufs := make([][]byte, dev.BatchSize())
	sizes := make([]int, len(bufs))
	for i := range bufs {
		bufs[i] = make([]byte, tunOffset+65535)
	}
	for {
		n, err := dev.Read(bufs, sizes, tunOffset)
		if err != nil {
			return
		}
		for i := 0; i < n; i++ {
			pkt := bufs[i][tunOffset : tunOffset+sizes[i]]
			if reply := icmp6Unreachable(pkt); reply != nil {
				out := make([]byte, tunOffset+len(reply))
				copy(out[tunOffset:], reply)
				_, _ = dev.Write([][]byte{out}, tunOffset)
			}
		}
	}
}

func TestICMP6LiveFastFailure(t *testing.T) {
	if os.Getenv("USATYI_ICMP6_LIVE") != "1" {
		t.Skip("нет USATYI_ICMP6_LIVE=1")
	}
	if os.Geteuid() != 0 {
		t.Skip("нужен root (TUN)")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("нет curl")
	}
	dev, err := awgtun.CreateTUN("usatyi6", 1280)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	name, err := dev.Name()
	if err != nil {
		t.Fatal(err)
	}
	runIP(t, "link", "set", name, "up")
	runIP(t, "-6", "addr", "add", "fd66::2/64", "dev", name)
	runIP(t, "-6", "route", "add", "default", "dev", name)

	// Было раньше: пакеты молча отбрасываются, curl ждёт таймаут.
	d1, msg1 := curl6(t)
	t.Logf("без ответа (как раньше): %v, %s", d1.Round(time.Millisecond), msg1)

	// Стало: программа отвечает ICMPv6, curl получает ошибку сразу.
	go answerer(dev)
	d2, msg2 := curl6(t)
	t.Logf("с ответом ICMPv6: %v, %s", d2.Round(time.Millisecond), msg2)
	if strings.Contains(msg2, "неожиданно") {
		t.Fatal(msg2)
	}
	if d2 > 2*time.Second {
		t.Fatalf("с ответом curl всё равно ждал %v", d2)
	}
}
