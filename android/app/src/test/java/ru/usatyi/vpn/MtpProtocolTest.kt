package ru.usatyi.vpn

import java.io.Closeable
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.security.SecureRandom
import java.util.Base64
import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assume.assumeTrue
import org.junit.Test

class MtpProtocolTest {

    private val uid = ByteArray(8) { (it + 1).toByte() }
    private val secret = ByteArray(32) { (it * 3).toByte() }

    private fun b64(b: ByteArray): String = Base64.getEncoder().encodeToString(b)
    private fun b64url(b: ByteArray): String = Base64.getUrlEncoder().withoutPadding().encodeToString(b)

    private fun sampleKey(): ServerKey = ServerKey(
        name = "Вася",
        host = "203.0.113.5",
        uid = uid,
        secret = secret,
        ports = mapOf("udp" to 443, "tcp" to 8443, "mask" to 443, "wg" to 51820, "ss" to 20001),
        wg = WgParams(b64(ByteArray(32) { 7 }), b64(ByteArray(32) { 9 }), "10.9.0.2/32", "1.1.1.1"),
        ss = SsParams("chacha20-ietf-poly1305", "пароль-123"),
    )

    // ---------- ключи

    @Test
    fun keyV2RoundTrip() {
        val text = KeyCodec.encode(sampleKey())
        assertTrue(text.startsWith("mtp://"))
        val back = KeyCodec.decode("  $text \n")
        assertEquals("Вася", back.name)
        assertEquals("203.0.113.5", back.host)
        assertArrayEquals(uid, back.uid)
        assertArrayEquals(secret, back.secret)
        assertEquals(8443, back.portFor(Transport.MTP2))
        assertEquals(51820, back.portFor(Transport.WG))
        assertEquals("10.9.0.2/32", back.wg?.address)
        assertEquals("пароль-123", back.ss?.password)
        assertTrue(back.supports(Transport.SS))
    }

    @Test
    fun keyV1StillDecodes() {
        val j = JSONObject().put("v", 1).put("h", "198.51.100.7").put("p", 4433)
            .put("u", uid.toHex()).put("s", b64url(secret)).put("n", "Петя")
        val k = KeyCodec.decode("mtp://" + b64url(j.toString().toByteArray(Charsets.UTF_8)))
        assertEquals(4433, k.portFor(Transport.MTP1))
        assertFalse(k.supports(Transport.WG))
        assertFalse(k.supports(Transport.MTP2))
    }

    @Test(expected = IllegalArgumentException::class)
    fun keyRejectsGarbage() {
        KeyCodec.decode("это не ключ")
    }

    @Test
    fun ssLinkCarriesMethodPasswordHostAndPort() {
        val link = ssLink(sampleKey())!!
        assertTrue(link.startsWith("ss://"))
        val body = link.removePrefix("ss://")
        val userinfo = body.substringBefore("@")
        val decoded = String(Base64.getUrlDecoder().decode(userinfo), Charsets.UTF_8)
        assertEquals("chacha20-ietf-poly1305:пароль-123", decoded)
        assertTrue(body.contains("@203.0.113.5:20001#"))
    }

    // ---------- окно повторов

    @Test
    fun replayWindowRejectsDuplicatesAndOldPackets() {
        val w = ReplayWindow()
        assertTrue(w.ok(0L))
        w.mark(0L)
        assertFalse(w.ok(0L))
        assertTrue(w.ok(5L))
        w.mark(5L)
        assertTrue(w.ok(3L))
        w.mark(3L)
        assertFalse(w.ok(3L))
        assertTrue(w.ok(4L))
        w.mark(100L)
        assertFalse(w.ok(36L)) // старше окна в 64 пакета
        assertTrue(w.ok(37L))
        assertFalse(w.ok(5L))
    }

    // ---------- криптография: векторы посчитаны на сервере (Python cryptography)

    @Test
    fun hkdfMatchesPythonServer() {
        val okm = Crypto.hkdf("ikm-material".toByteArray(), "salt-salt".toByteArray(), "mtp1 keys".toByteArray(), 64)
        assertEquals(
            "d8e9171bf3129e1a33ec9adf91fe90e46c9fc2654ae0a2ba860837ba1cc4606bacaa6c2bde2b51fa64abc8b86564a5b81fcc47ceb2a5af6a226bc154950cc98c",
            okm.toHex(),
        )
    }

    @Test
    fun chacha20Poly1305MatchesPythonServer() {
        val key = ByteArray(32) { it.toByte() }
        val aad = ByteArray(13) { it.toByte() }
        val ct = Crypto.seal(key, 5L, aad, "hello world, mtp/1".toByteArray())
        assertEquals("a4ad8f704dbfee5cdbe2279cec717d0cbf2f22ab3f774c76d069157514d6d4ce4608", ct.toHex())
        assertEquals("hello world, mtp/1", String(Crypto.open(key, 5L, aad, ct)!!, Charsets.UTF_8))
        assertNull(Crypto.open(key, 6L, aad, ct))
    }

    @Test
    fun tagMatchesPythonServer() {
        val tag = Crypto.tag16(ByteArray(32) { 'k'.code.toByte() }, "mtp1-hello".toByteArray(), "abc".toByteArray())
        assertEquals("f58c68bec3be5d3f184a44b2249801fc", tag.toHex())
    }

    @Test
    fun publicKeyEncodingRoundTrip() {
        val kp = Crypto.newKeyPair()
        val enc = Crypto.encodePub(kp.public)
        assertEquals(65, enc.size)
        assertEquals(4, enc[0].toInt())
        assertArrayEquals(enc, Crypto.encodePub(Crypto.decodePub(enc)))
    }

    // ---------- разметка кадров MTP/2 и MTP/3

    @Test
    fun tcpFramingRoundTripAndPartialFrames() {
        val frame = ByteArray(300) { it.toByte() }
        val wire = Framing.encodeTcp(frame)
        assertNull(Framing.tryParse(wire, 1, false))
        assertNull(Framing.tryParse(wire, wire.size - 1, false)) // кадр пришёл не весь
        val parsed = Framing.tryParse(wire, wire.size, false)!!
        assertArrayEquals(frame, parsed.first)
        assertEquals(wire.size, parsed.second)
    }

    @Test
    fun maskFramingLooksLikeTlsRecords() {
        val rnd = SecureRandom()
        val data = byteArrayOf(0x04) + ByteArray(100) { 5 }   // DATA
        val hello = byteArrayOf(0x01) + ByteArray(97) { 6 }   // HELLO
        val wire = Framing.encodeMask(data, rnd)
        assertEquals(0x17, wire[0].toInt() and 0xff)
        assertEquals(3, wire[1].toInt())
        assertEquals(3, wire[2].toInt())
        assertArrayEquals(data, Framing.tryParse(wire, wire.size, true)!!.first)

        val wireHello = Framing.encodeMask(hello, rnd)
        assertEquals(0x16, wireHello[0].toInt() and 0xff)
        assertArrayEquals(hello, Framing.tryParse(wireHello, wireHello.size, true)!!.first)

        assertNull(Framing.tryParse(wire, 4, true))
        // два кадра подряд: первый отрезается целиком, второй остаётся в буфере
        val two = wire + wireHello
        val first = Framing.tryParse(two, two.size, true)!!
        assertEquals(wire.size, first.second)
        val rest = two.copyOfRange(first.second, two.size)
        assertArrayEquals(hello, Framing.tryParse(rest, rest.size, true)!!.first)
    }

    // ---------- живой тест: ./gradlew testDebugUnitTest -Pmtp.test.key="mtp://..."

    /**
     * Проверяет каждый MTP-канал, который есть в ключе: рукопожатие и пинг 10.8.0.1 через туннель.
     * Без ключа тест пропускается.
     */
    @Test
    fun liveTransportsPingThroughTunnel() {
        val keyText = System.getProperty("mtp.test.key")
        assumeTrue("задай -Pmtp.test.key=mtp://... для проверки живого сервера", !keyText.isNullOrBlank())
        val key = KeyCodec.decode(keyText!!)
        for (t in listOf(Transport.MTP1, Transport.MTP2, Transport.MTP3)) {
            if (key.supports(t)) pingVia(t, key)
        }
    }

    private fun openLink(t: Transport, key: ServerKey): Link {
        val port = key.portFor(t)!!
        return when (t) {
            Transport.MTP1 -> UdpLink.open(key.host, port) {}
            Transport.MTP2 -> TcpLink.open(key.host, port, false) {}
            Transport.MTP3 -> TcpLink.open(key.host, port, true) {}
            else -> error("не канал MTP: $t")
        }
    }

    private fun pingVia(t: Transport, key: ServerKey) {
        val link = openLink(t, key)
        try {
            val client = MtpClient(key, link)
            val w = client.handshakeFor(t)
            val src = w.ip.joinToString(".") { (it.toInt() and 0xff).toString() }
            val ident = 0x5151 + t.ordinal
            val seq = 7
            val req = buildIcmpEcho(src, "10.8.0.1", ident, seq)
            link.send(client.encrypt(Mtp.K_IP, req, 0, req.size))
            assertTrue("${t.label}: сервер не ответил на пинг через туннель",
                waitEchoReply(client, link, "10.8.0.1", ident, seq))
        } finally {
            link.close()
        }
    }

    /**
     * Медленная сеть: первый WELCOME по TCP приходит через 7 с, то есть позже окна рукопожатия (6 с).
     * Клиент повторяет HELLO на том же соединении. Сервер не должен рвать соединение: раньше он рвал его,
     * и телефон показывал «ошибка туннеля: failed EBADF». Сессия по второму WELCOME должна пропускать пинг.
     * Без ключа тест пропускается.
     */
    @Test
    fun liveTcpSurvivesLateWelcomeAndRepeatedHello() {
        val keyText = System.getProperty("mtp.test.key")
        assumeTrue("задай -Pmtp.test.key=mtp://... для проверки живого сервера", !keyText.isNullOrBlank())
        val key = KeyCodec.decode(keyText!!)
        for (t in listOf(Transport.MTP2, Transport.MTP3)) {
            if (!key.supports(t)) continue
            val proxy = DelayingProxy(key.host, key.portFor(t)!!, delayMs = 7000L)
            try {
                val link = TcpLink.open("127.0.0.1", proxy.localPort, t == Transport.MTP3) {}
                try {
                    val client = MtpClient(key, link)
                    val w = client.handshakeFor(t)
                    val src = w.ip.joinToString(".") { (it.toInt() and 0xff).toString() }
                    val ident = 0x6161 + t.ordinal
                    val req = buildIcmpEcho(src, "10.8.0.1", ident, 3)
                    link.send(client.encrypt(Mtp.K_IP, req, 0, req.size))
                    assertTrue("${t.label}: после повторного HELLO сессия не отвечает",
                        waitEchoReply(client, link, "10.8.0.1", ident, 3))
                } finally {
                    link.close()
                }
            } finally {
                proxy.close()
            }
        }
    }
}

/**
 * TCP-прокси для проверки медленной сети: первая порция данных от сервера задерживается на delayMs.
 * Клиент подключается к localPort, прокси соединяется с настоящим сервером.
 */
private class DelayingProxy(host: String, port: Int, private val delayMs: Long) : Closeable {
    private val listener = ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))
    private val sockets = mutableListOf<Socket>()
    val localPort: Int get() = listener.localPort

    init {
        Thread {
            try {
                val client = listener.accept()
                val upstream = Socket(host, port)
                synchronized(sockets) {
                    sockets.add(client)
                    sockets.add(upstream)
                }
                Thread { pipe(client.getInputStream(), upstream.getOutputStream(), 0L) }
                    .apply { isDaemon = true }
                    .start()
                pipe(upstream.getInputStream(), client.getOutputStream(), delayMs)
            } catch (e: Exception) {
                // соединение закрыто тестом
            }
        }.apply { isDaemon = true }.start()
    }

    /** Копирует байты. Задержка применяется только к первой порции. */
    private fun pipe(from: InputStream, to: OutputStream, firstDelay: Long) {
        val buf = ByteArray(8192)
        var delay = firstDelay
        try {
            while (true) {
                val n = from.read(buf)
                if (n < 0) break
                if (delay > 0L) {
                    Thread.sleep(delay)
                    delay = 0L
                }
                to.write(buf, 0, n)
                to.flush()
            }
        } catch (e: Exception) {
            // обрыв соединения
        }
    }

    override fun close() {
        listener.close()
        synchronized(sockets) { sockets.forEach { runCatching { it.close() } } }
    }
}

private fun checksum(b: ByteArray): Int {
    var sum = 0L
    var i = 0
    while (i + 1 < b.size) {
        sum += (((b[i].toInt() and 0xff) shl 8) or (b[i + 1].toInt() and 0xff)).toLong()
        i += 2
    }
    if (i < b.size) sum += ((b[i].toInt() and 0xff) shl 8).toLong()
    while ((sum ushr 16) != 0L) sum = (sum and 0xffffL) + (sum ushr 16)
    return (sum.inv() and 0xffffL).toInt()
}

private fun buildIcmpEcho(src: String, dst: String, ident: Int, seq: Int): ByteArray {
    val payload = "usatyi-test".toByteArray(Charsets.US_ASCII)
    val icmp = ByteArray(8 + payload.size)
    icmp[0] = 8
    icmp[1] = 0
    icmp[4] = (ident shr 8).toByte()
    icmp[5] = ident.toByte()
    icmp[6] = (seq shr 8).toByte()
    icmp[7] = seq.toByte()
    System.arraycopy(payload, 0, icmp, 8, payload.size)
    val ic = checksum(icmp)
    icmp[2] = (ic shr 8).toByte()
    icmp[3] = ic.toByte()

    val ip = ByteArray(20 + icmp.size)
    ip[0] = 0x45
    ip[2] = (ip.size shr 8).toByte()
    ip[3] = ip.size.toByte()
    ip[6] = 0x40
    ip[8] = 64
    ip[9] = 1
    System.arraycopy(InetAddress.getByName(src).address, 0, ip, 12, 4)
    System.arraycopy(InetAddress.getByName(dst).address, 0, ip, 16, 4)
    val hc = checksum(ip.copyOfRange(0, 20))
    ip[10] = (hc shr 8).toByte()
    ip[11] = hc.toByte()
    System.arraycopy(icmp, 0, ip, 20, icmp.size)
    return ip
}

private fun isEchoReply(pkt: ByteArray, src: String, ident: Int, seq: Int): Boolean {
    if (pkt.size < 28 || pkt[9].toInt() != 1) return false
    val from = InetAddress.getByAddress(pkt.copyOfRange(12, 16)).hostAddress
    if (from != src) return false
    val ihl = (pkt[0].toInt() and 0x0f) * 4
    if (pkt.size < ihl + 8) return false
    val type = pkt[ihl].toInt() and 0xff
    val rid = ((pkt[ihl + 4].toInt() and 0xff) shl 8) or (pkt[ihl + 5].toInt() and 0xff)
    val rseq = ((pkt[ihl + 6].toInt() and 0xff) shl 8) or (pkt[ihl + 7].toInt() and 0xff)
    return type == 0 && rid == ident && rseq == seq
}

private fun waitEchoReply(client: MtpClient, link: Link, src: String, ident: Int, seq: Int): Boolean {
    val end = System.currentTimeMillis() + 5_000L
    while (System.currentTimeMillis() < end) {
        val f = link.recv(500) ?: continue
        val r = client.decrypt(f, f.size) ?: continue
        if (r.first == Mtp.K_IP && isEchoReply(r.second, src, ident, seq)) return true
    }
    return false
}
