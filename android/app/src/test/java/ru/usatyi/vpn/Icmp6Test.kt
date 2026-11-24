package ru.usatyi.vpn

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/** Проверки ответа ICMPv6 «адрес недоступен» — того же, что шлёт Windows-версия. */
class Icmp6Test {

    private fun ipv6(src: ByteArray, dst: ByteArray, nextHeader: Int, payload: ByteArray): ByteArray {
        val out = ByteArray(40 + payload.size)
        out[0] = 0x60
        out[4] = (payload.size shr 8).toByte()
        out[5] = (payload.size and 0xff).toByte()
        out[6] = nextHeader.toByte()
        out[7] = 64
        System.arraycopy(src, 0, out, 8, 16)
        System.arraycopy(dst, 0, out, 24, 16)
        System.arraycopy(payload, 0, out, 40, payload.size)
        return out
    }

    private fun addr(fill: Int, last: Int = fill): ByteArray {
        val a = ByteArray(16) { fill.toByte() }
        a[15] = last.toByte()
        return a
    }

    @Test
    fun `ipv4 и короткие пакеты не трогаем`() {
        assertNull(Icmp6.unreachable(ByteArray(40))) // нули: версия 0
        val v4 = ByteArray(60)
        v4[0] = 0x45
        assertNull(Icmp6.unreachable(v4))
        assertNull(Icmp6.unreachable(ByteArray(39) { 0x60 }))
    }

    @Test
    fun `multicast и неуказанный источник не получают ответ`() {
        val mc = addr(0xff, 1)
        assertNull(Icmp6.unreachable(ipv6(addr(1), mc, 6, ByteArray(20))))
        assertNull(Icmp6.unreachable(ipv6(ByteArray(16), addr(2), 6, ByteArray(20))))
    }

    @Test
    fun `на icmpv6 ошибкой не отвечаем`() {
        assertNull(Icmp6.unreachable(ipv6(addr(1), addr(2), 58, ByteArray(20))))
    }

    @Test
    fun `ответ содержит ошибку и исходный пакет`() {
        val src = addr(0x20, 7)
        val dst = addr(0xfd, 2)
        val payload = ByteArray(64) { it.toByte() }
        val orig = ipv6(src, dst, 6, payload)

        val reply = Icmp6.unreachable(orig)
        assertNotNull(reply)
        reply!!
        assertEquals(0x60, reply[0].toInt() and 0xff)
        assertEquals(58, reply[6].toInt() and 0xff)
        assertEquals(64, reply[7].toInt() and 0xff)
        assertArrayEquals(dst, reply.copyOfRange(8, 24)) // ответ «от» недоступного адреса
        assertArrayEquals(src, reply.copyOfRange(24, 40))
        assertEquals(1, reply[40].toInt() and 0xff) // Destination Unreachable
        assertEquals(0, reply[41].toInt() and 0xff) // no route
        // длина сообщения в payload length = 8 + исходный пакет
        val plen = ((reply[4].toInt() and 0xff) shl 8) or (reply[5].toInt() and 0xff)
        assertEquals(8 + orig.size, plen)
        assertArrayEquals(orig, reply.copyOfRange(48, 48 + orig.size))

        // контрольная сумма: пересчёт по сообщению с полем суммы даёт 0
        val msg = reply.copyOfRange(40, reply.size)
        assertEquals(0, Icmp6.checksum(reply.copyOfRange(8, 24), reply.copyOfRange(24, 40), msg))
    }

    @Test
    fun `большой пакет укладывается в 1280 байт`() {
        val orig = ipv6(addr(1), addr(2), 6, ByteArray(3000))
        val reply = Icmp6.unreachable(orig)
        assertNotNull(reply)
        assertEquals(1280, reply!!.size)
        val msg = reply.copyOfRange(40, reply.size)
        assertEquals(0, Icmp6.checksum(reply.copyOfRange(8, 24), reply.copyOfRange(24, 40), msg))
    }
}
