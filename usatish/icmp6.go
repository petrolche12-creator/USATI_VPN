package main

// Ответ на IPv6-пакеты из адаптера. IPv6 через VPN не передаётся (сервер его не принимает), но и молча
// отбрасывать его нельзя: приложения, которые сначала пробуют IPv6, ждали бы таймаут (иногда десятки секунд).
// Поэтому в ответ отправляется ICMPv6 «адрес недоступен»: приложение сразу получает ошибку и переходит на IPv4.

import "encoding/binary"

const (
	icmp6TypeUnreachable = 1
	icmp6CodeNoRoute     = 0
	icmp6NextHeader      = 58 // ICMPv6
	icmp6MaxPacket       = 1280
)

// icmp6Unreachable собирает ICMPv6 «адрес недоступен» в ответ на IPv6-пакет orig (без отступа для адаптера).
// Возвращает nil, если отвечать нельзя: это не IPv6, multicast, адрес отправителя «неуказанный»
// или сам пакет ICMPv6 (на ICMP-сообщения ошибкой не отвечают, RFC 4443, раздел 2.4).
func icmp6Unreachable(orig []byte) []byte {
	if len(orig) < 40 || orig[0]>>4 != 6 {
		return nil
	}
	src := orig[8:24]
	dst := orig[24:40]
	if dst[0] == 0xff || src[0] == 0xff || isZero(src) {
		return nil
	}
	if orig[6] == icmp6NextHeader {
		return nil
	}
	inv := orig
	if room := icmp6MaxPacket - 40 - 8; len(inv) > room {
		inv = inv[:room]
	}
	plen := 8 + len(inv)
	out := make([]byte, 40+plen)
	out[0] = 0x60 // версия 6
	binary.BigEndian.PutUint16(out[4:6], uint16(plen))
	out[6] = icmp6NextHeader
	out[7] = 64          // hop limit
	copy(out[8:24], dst) // ответ «от» адреса, до которого нельзя дойти
	copy(out[24:40], src)
	msg := out[40:]
	msg[0] = icmp6TypeUnreachable
	msg[1] = icmp6CodeNoRoute
	copy(msg[8:], inv)
	binary.BigEndian.PutUint16(msg[2:4], icmp6Checksum(out[8:24], out[24:40], msg))
	return out
}

// icmp6Checksum — контрольная сумма ICMPv6: псевдозаголовок (адреса, длина, тип 58) и само сообщение.
// Поле контрольной суммы в msg должно быть нулевым.
func icmp6Checksum(src, dst, msg []byte) uint16 {
	var sum uint32
	add := func(b []byte) {
		for i := 0; i+1 < len(b); i += 2 {
			sum += uint32(b[i])<<8 | uint32(b[i+1])
		}
		if len(b)%2 == 1 {
			sum += uint32(b[len(b)-1]) << 8
		}
	}
	add(src)
	add(dst)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(msg)))
	add(length[:])
	add([]byte{0, 0, 0, icmp6NextHeader})
	add(msg)
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
