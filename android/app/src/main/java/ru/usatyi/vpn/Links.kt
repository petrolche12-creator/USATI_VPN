package ru.usatyi.vpn

import java.io.IOException
import java.io.Closeable
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.PortUnreachableException
import java.net.Socket
import java.net.SocketTimeoutException
import java.security.SecureRandom

/**
 * Канал, по которому идут кадры MTP. Один и тот же протокол работает поверх трёх каналов:
 * UDP (MTP/1), TCP с длиной кадра (MTP/2) и TCP с записями, похожими на TLS (MTP/3).
 */
interface Link : Closeable {
    /** Отправляет один кадр протокола. Для TCP канал сам добавляет разметку кадра. */
    fun send(frame: ByteArray)

    /** Следующий кадр или null, если за timeoutMs ничего не пришло. */
    fun recv(timeoutMs: Int): ByteArray?
}

class UdpLink private constructor(private val sock: DatagramSocket) : Link {
    private val rx = ByteArray(2048)
    private val dp = DatagramPacket(rx, rx.size)

    override fun send(frame: ByteArray) {
        sock.send(DatagramPacket(frame, frame.size))
    }

    override fun recv(timeoutMs: Int): ByteArray? {
        sock.soTimeout = timeoutMs.coerceAtLeast(1)
        dp.length = rx.size
        return try {
            sock.receive(dp)
            rx.copyOf(dp.length)
        } catch (e: SocketTimeoutException) {
            null
        } catch (e: PortUnreachableException) {
            null // сервер перезапускается: продолжаем ждать
        }
    }

    override fun close() {
        sock.close()
    }

    companion object {
        fun open(host: String, port: Int, protect: (DatagramSocket) -> Unit): UdpLink {
            val s = DatagramSocket()
            protect(s)
            s.connect(InetAddress.getByName(host), port)
            return UdpLink(s)
        }
    }
}

class TcpLink private constructor(private val sock: Socket, private val mask: Boolean) : Link {
    private val input = sock.getInputStream()
    private val output = sock.getOutputStream()
    private val rnd = SecureRandom()
    private var buf = ByteArray(8192)
    private var len = 0

    override fun send(frame: ByteArray) {
        val data = if (mask) Framing.encodeMask(frame, rnd) else Framing.encodeTcp(frame)
        synchronized(output) {
            output.write(data)
            output.flush()
        }
    }

    override fun recv(timeoutMs: Int): ByteArray? {
        val deadline = System.currentTimeMillis() + timeoutMs
        while (true) {
            val parsed = Framing.tryParse(buf, len, mask)
            if (parsed != null) {
                System.arraycopy(buf, parsed.second, buf, 0, len - parsed.second)
                len -= parsed.second
                return parsed.first
            }
            val left = deadline - System.currentTimeMillis()
            if (left <= 0L) return null
            if (len == buf.size) buf = buf.copyOf(buf.size * 2)
            sock.soTimeout = left.toInt().coerceAtLeast(1)
            val n = try {
                input.read(buf, len, buf.size - len)
            } catch (e: SocketTimeoutException) {
                return null
            }
            if (n < 0) throw IOException("соединение закрыто сервером")
            len += n
        }
    }

    override fun close() {
        sock.close()
    }

    companion object {
        fun open(host: String, port: Int, mask: Boolean, protect: (Socket) -> Unit): TcpLink {
            val s = Socket()
            protect(s)
            s.tcpNoDelay = true
            s.connect(InetSocketAddress(host, port), 10_000)
            return TcpLink(s, mask)
        }
    }
}

/** Разметка кадров для TCP. Формат такой же, как в server/mtp.py (encode_frame, read_frame). */
internal object Framing {
    const val MAX_FRAME = 2048

    /** MTP/2: [длина 2, big-endian][кадр]. */
    fun encodeTcp(frame: ByteArray): ByteArray {
        val out = ByteArray(2 + frame.size)
        out[0] = (frame.size shr 8).toByte()
        out[1] = frame.size.toByte()
        System.arraycopy(frame, 0, out, 2, frame.size)
        return out
    }

    /**
     * MTP/3: запись [0x16 (рукопожатие) или 0x17 (данные), 0x03, 0x03][длина 2]
     * [паддинг-длина 1][кадр][случайный паддинг].
     */
    fun encodeMask(frame: ByteArray, rnd: SecureRandom): ByteArray {
        val rtype = if (frame[0].toInt() == Mtp.T_DATA) 0x17 else 0x16
        val pad = rnd.nextInt(48)
        val body = 1 + frame.size + pad
        val out = ByteArray(5 + body)
        out[0] = rtype.toByte()
        out[1] = 3
        out[2] = 3
        out[3] = (body shr 8).toByte()
        out[4] = body.toByte()
        out[5] = pad.toByte()
        System.arraycopy(frame, 0, out, 6, frame.size)
        val padBytes = ByteArray(pad)
        rnd.nextBytes(padBytes)
        System.arraycopy(padBytes, 0, out, 6 + frame.size, pad)
        return out
    }

    /**
     * Разбирает первый кадр в буфере. Возвращает пару (кадр, сколько байт занято)
     * или null, если кадр ещё пришёл не весь. Бросает IOException при неверном формате.
     */
    fun tryParse(buf: ByteArray, len: Int, mask: Boolean): Pair<ByteArray, Int>? {
        if (!mask) {
            if (len < 2) return null
            val n = ((buf[0].toInt() and 0xff) shl 8) or (buf[1].toInt() and 0xff)
            if (n == 0 || n > MAX_FRAME) throw IOException("неверная длина кадра")
            if (len < 2 + n) return null
            return Pair(buf.copyOfRange(2, 2 + n), 2 + n)
        }
        if (len < 5) return null
        val t = buf[0].toInt() and 0xff
        if ((t != 0x16 && t != 0x17) || buf[1].toInt() != 3 || buf[2].toInt() != 3) {
            throw IOException("сервер прислал не запись маскировки")
        }
        val n = ((buf[3].toInt() and 0xff) shl 8) or (buf[4].toInt() and 0xff)
        if (n < 2 || n > MAX_FRAME + 64) throw IOException("неверная длина записи")
        if (len < 5 + n) return null
        val pad = buf[5].toInt() and 0xff
        if (pad >= n - 1) throw IOException("неверный паддинг")
        return Pair(buf.copyOfRange(6, 5 + n - pad), 5 + n)
    }
}
