package ru.usatyi.vpn

/**
 * Ответ на IPv6-пакеты из интерфейса VPN. IPv6 через VPN не передаётся (сервер его не принимает),
 * но и молча отбрасывать его нельзя: приложения, которые сначала пробуют IPv6, ждали бы таймаут —
 * на мобильном интернете это выглядело как «будто интернета нет». Поэтому в ответ пишется ICMPv6
 * «адрес недоступен»: приложение сразу получает ошибку и переходит на IPv4.
 *
 * Побайтово повторяет Windows-версию (windows/icmp6.go), чтобы оба клиента отвечали одинаково.
 */
object Icmp6 {
    private const val TYPE_UNREACHABLE = 1
    private const val CODE_NO_ROUTE = 0
    private const val NEXT_HEADER = 58 // ICMPv6
    private const val MAX_PACKET = 1280 // минимальная гарантиярованная MTU IPv6

    /**
     * Собирает ICMPv6 «адрес недоступен» в ответ на пакет [orig] длиной [len].
     * Возвращает null, если отвечать нельзя: это не IPv6, multicast, адрес отправителя «неуказанный»
     * или сам пакет ICMPv6 (на ICMP-сообщения ошибкой не отвечают, RFC 4443, раздел 2.4).
     */
    @JvmStatic
    fun unreachable(orig: ByteArray, len: Int = orig.size): ByteArray? {
        if (len < 40 || (orig[0].toInt() and 0xff) shr 4 != 6) return null
        val src = orig.copyOfRange(8, 24)
        val dst = orig.copyOfRange(24, 40)
        if (dst[0] == 0xff.toByte() || src[0] == 0xff.toByte() || src.all { it == 0.toByte() }) return null
        if (orig[6].toInt() and 0xff == NEXT_HEADER) return null

        var invLen = len
        val room = MAX_PACKET - 40 - 8
        if (invLen > room) invLen = room
        val plen = 8 + invLen

        val out = ByteArray(40 + plen)
        out[0] = 0x60.toByte() // версия 6
        out[4] = (plen shr 8).toByte()
        out[5] = (plen and 0xff).toByte()
        out[6] = NEXT_HEADER.toByte()
        out[7] = 64 // hop limit
        System.arraycopy(dst, 0, out, 8, 16) // ответ «от» адреса, до которого нельзя дойти
        System.arraycopy(src, 0, out, 24, 16)

        val msg = ByteArray(plen)
        msg[0] = TYPE_UNREACHABLE.toByte()
        msg[1] = CODE_NO_ROUTE.toByte()
        System.arraycopy(orig, 0, msg, 8, invLen)
        val sum = checksum(out.copyOfRange(8, 24), out.copyOfRange(24, 40), msg)
        msg[2] = (sum shr 8).toByte()
        msg[3] = (sum and 0xff).toByte()
        System.arraycopy(msg, 0, out, 40, plen)
        return out
    }

    /** Контрольная сумма ICMPv6: псевдозаголовок (адреса, длина, тип 58) и само сообщение. */
    @JvmStatic
    fun checksum(src: ByteArray, dst: ByteArray, msg: ByteArray): Int {
        var sum = 0L
        fun add(b: ByteArray) {
            var i = 0
            while (i + 1 < b.size) {
                sum += (((b[i].toInt() and 0xff) shl 8) or (b[i + 1].toInt() and 0xff)).toLong()
                i += 2
            }
            if (b.size % 2 == 1) sum += (b[b.size - 1].toInt() and 0xff) shl 8
        }
        add(src)
        add(dst)
        add(intBytes(msg.size))
        add(byteArrayOf(0, 0, 0, NEXT_HEADER.toByte()))
        add(msg)
        while (sum shr 16 != 0L) sum = (sum and 0xffffL) + (sum shr 16)
        return sum.toInt().inv() and 0xffff
    }

    private fun intBytes(v: Int) = byteArrayOf(
        (v shr 24).toByte(), (v shr 16).toByte(), (v shr 8).toByte(), v.toByte()
    )
}
