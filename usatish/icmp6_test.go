package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// ipv6Packet собирает IPv6-пакет с UDP-заголовком (для проверок ответа).
func ipv6Packet(src, dst []byte, next byte, payloadLen int) []byte {
	p := make([]byte, 40+payloadLen)
	p[0] = 0x60
	binary.BigEndian.PutUint16(p[4:6], uint16(payloadLen))
	p[6] = next
	p[7] = 64
	copy(p[8:24], src)
	copy(p[24:40], dst)
	for i := 40; i < len(p); i++ {
		p[i] = byte(i)
	}
	return p
}

var (
	testSrc = []byte{0xfd, 0x10, 0x00, 0x08, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2} // адрес в туннеле
	testDst = []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1} // внешний IPv6
)

func TestICMP6UnreachableFields(t *testing.T) {
	orig := ipv6Packet(testSrc, testDst, 17, 40)
	out := icmp6Unreachable(orig)
	if out == nil {
		t.Fatal("ответ не собран")
	}
	if out[0]>>4 != 6 {
		t.Fatalf("версия %d, нужна 6", out[0]>>4)
	}
	if got := int(binary.BigEndian.Uint16(out[4:6])); got != len(out)-40 {
		t.Fatalf("длина полезной части %d, в пакете %d", got, len(out)-40)
	}
	if out[6] != icmp6NextHeader || out[7] != 64 {
		t.Fatalf("следующий заголовок %d, hop limit %d", out[6], out[7])
	}
	if !bytes.Equal(out[8:24], testDst) {
		t.Fatal("источник ответа должен быть адресом, до которого нельзя дойти")
	}
	if !bytes.Equal(out[24:40], testSrc) {
		t.Fatal("получатель ответа должен быть отправителем исходного пакета")
	}
	if out[40] != icmp6TypeUnreachable || out[41] != icmp6CodeNoRoute {
		t.Fatalf("тип %d код %d", out[40], out[41])
	}
	if !bytes.Equal(out[48:], orig) {
		t.Fatal("в ответе должен быть исходный пакет")
	}
	// контрольная сумма верна, если сумма по всему сообщению с полем суммы даёт 0
	if sum := icmp6Checksum(out[8:24], out[24:40], out[40:]); sum != 0 {
		t.Fatalf("контрольная сумма неверна: проверка дала %#x", sum)
	}
}

func TestICMP6UnreachableSkips(t *testing.T) {
	ipv4 := []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 17, 0, 0, 10, 8, 0, 2, 1, 1, 1, 1}
	cases := map[string][]byte{
		"ipv4":            ipv4,
		"short":           ipv6Packet(testSrc, testDst, 17, 0)[:30],
		"icmpv6":          ipv6Packet(testSrc, testDst, icmp6NextHeader, 8),
		"multicast dst":   ipv6Packet(testSrc, append([]byte{0xff, 0x02}, make([]byte, 13)...), 17, 8),
		"unspecified src": ipv6Packet(make([]byte, 16), testDst, 17, 8),
	}
	for name, pkt := range cases {
		if out := icmp6Unreachable(pkt); out != nil {
			t.Errorf("%s: ответа быть не должно", name)
		}
	}
}

func TestICMP6UnreachableLimitsSize(t *testing.T) {
	orig := ipv6Packet(testSrc, testDst, 17, 2000)
	out := icmp6Unreachable(orig)
	if out == nil || len(out) > icmp6MaxPacket {
		t.Fatalf("размер ответа %d, лимит %d", len(out), icmp6MaxPacket)
	}
	if sum := icmp6Checksum(out[8:24], out[24:40], out[40:]); sum != 0 {
		t.Fatalf("контрольная сумма неверна для усечённого ответа: %#x", sum)
	}
}

func TestICMP6ChecksumOddLength(t *testing.T) {
	// нечётная длина сообщения: последний байт дополняется нулём
	msg := []byte{1, 0, 0, 0, 0xab}
	sum := icmp6Checksum(testSrc, testDst, msg)
	msg[2], msg[3] = byte(sum>>8), byte(sum)
	if got := icmp6Checksum(testSrc, testDst, msg); got != 0 {
		t.Fatalf("после вставки суммы проверка дала %#x", got)
	}
}
