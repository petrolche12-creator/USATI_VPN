package ru.usatyi.vpn

import java.math.BigInteger
import java.net.URLEncoder
import java.security.AlgorithmParameters
import java.security.GeneralSecurityException
import java.security.KeyFactory
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.PrivateKey
import java.security.PublicKey
import java.security.SecureRandom
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
import javax.crypto.Mac
import javax.crypto.spec.IvParameterSpec
import javax.crypto.spec.SecretKeySpec
import org.json.JSONObject

/** Константы протокола MTP/1-3. Должны совпадать с server/mtp.py. */
object Mtp {
    const val T_HELLO = 0x01
    const val T_WELCOME = 0x02
    const val T_REJECT = 0x03
    const val T_DATA = 0x04

    const val K_IP = 0x00
    const val K_KEEPALIVE = 0x01
    const val K_STATUS = 0x02
    const val K_KICK = 0x03

    const val HELLO_LEN = 98
    const val WELCOME_LEN = 96
    const val REJECT_LEN = 18
    const val HDR_LEN = 13
    const val MIN_DATA_LEN = 30

    val LBL_HELLO: ByteArray = "mtp1-hello".toByteArray(Charsets.US_ASCII)
    val LBL_WELCOME: ByteArray = "mtp1-welcome".toByteArray(Charsets.US_ASCII)
    val LBL_REJECT: ByteArray = "mtp1-reject".toByteArray(Charsets.US_ASCII)
    val LBL_KEYS: ByteArray = "mtp1 keys".toByteArray(Charsets.US_ASCII)

    fun reasonText(code: Int): String = when (code) {
        1 -> "ключ отключён администратором"
        2 -> "срок действия ключа истёк"
        3 -> "лимит трафика исчерпан"
        else -> "сервер не пустил"
    }
}

open class MtpException(message: String) : Exception(message)

class MtpRejected(val reason: Int) : MtpException(Mtp.reasonText(reason))

class MtpKicked(val reason: Int) : MtpException(Mtp.reasonText(reason))

class MtpTimeout : MtpException("сервер не отвечает: проверь адрес и порт")

/** Способы подключения: три свои (MTP/1-3) и два стандартных. */
enum class Transport(val label: String, val portKey: String, val own: Boolean, val hint: String) {
    MTP1("MTP/1 · UDP", "udp", true, "основной способ, самый быстрый; UDP 443"),
    MTP2("MTP/2 · TCP", "tcp", true, "тот же протокол поверх TCP, для сетей, где режут UDP; TCP 8443"),
    MTP3("MTP/3 · маскировка", "mask", true, "тот же протокол, замаскирован под TLS; TCP 443"),
    WG("WireGuard", "wg", false, "стандартный WireGuard; в этой сборке выключен — выберите MTP"),
    SS("Shadowsocks", "ss", false, "стандартный Shadowsocks: туннель поднимает приложение Shadowsocks, здесь копируется ссылка ss://");

    val isMtp: Boolean get() = this == MTP1 || this == MTP2 || this == MTP3
}

class WgParams(val privateKey: String, val serverPublicKey: String, val address: String, val dns: String)

class SsParams(val method: String, val password: String)

/**
 * Ключ доступа. Версия 1 — только MTP/1 (UDP). Версия 2 — все способы подключения:
 * ports: порт каждого способа (udp, tcp, mask, wg, ss); wg и ss — их параметры.
 */
class ServerKey(
    val name: String,
    val host: String,
    val uid: ByteArray,
    val secret: ByteArray,
    val ports: Map<String, Int>,
    val wg: WgParams?,
    val ss: SsParams?,
) {
    fun portFor(t: Transport): Int? = ports[t.portKey]

    fun supports(t: Transport): Boolean = when (t) {
        Transport.WG -> wg != null && portFor(t) != null
        Transport.SS -> ss != null && portFor(t) != null
        else -> portFor(t) != null
    }
}

/** Строка ключа: mtp:// + base64url(JSON). */
object KeyCodec {
    private const val PREFIX = "mtp://"

    fun encode(k: ServerKey): String {
        val j = JSONObject()
            .put("v", 2)
            .put("n", k.name)
            .put("h", k.host)
            .put("u", k.uid.toHex())
            .put("s", b64u(k.secret))
        val p = JSONObject()
        for ((name, port) in k.ports) p.put(name, port)
        j.put("p", p)
        k.wg?.let { w ->
            j.put("w", JSONObject().put("k", w.privateKey).put("s", w.serverPublicKey)
                .put("a", w.address).put("d", w.dns))
        }
        k.ss?.let { s -> j.put("ss", JSONObject().put("m", s.method).put("k", s.password)) }
        return PREFIX + b64u(j.toString().toByteArray(Charsets.UTF_8))
    }

    fun decode(text: String): ServerKey {
        val t = text.trim()
        require(t.startsWith(PREFIX)) { "ключ должен начинаться с mtp://" }
        val j = try {
            JSONObject(String(Base64.getUrlDecoder().decode(t.substring(PREFIX.length)), Charsets.UTF_8))
        } catch (e: Exception) {
            throw IllegalArgumentException("ключ повреждён")
        }
        val uid = hexToBytes(j.getString("u"))
        val secret = Base64.getUrlDecoder().decode(j.getString("s"))
        require(uid.size == 8 && secret.size == 32) { "ключ повреждён" }
        val host = j.getString("h")
        require(host.isNotBlank()) { "в ключе нет адреса сервера" }
        val pRaw = j.opt("p")
        val ports: Map<String, Int> = if (pRaw is JSONObject) {
            pRaw.keys().asSequence().associateWith { pRaw.getInt(it) }
        } else {
            mapOf("udp" to j.getInt("p"))
        }
        val wg = j.optJSONObject("w")?.let {
            WgParams(it.getString("k"), it.getString("s"), it.getString("a"), it.optString("d", "1.1.1.1"))
        }
        val ss = j.optJSONObject("ss")?.let { SsParams(it.getString("m"), it.getString("k")) }
        return ServerKey(j.optString("n", ""), host, uid, secret, ports, wg, ss)
    }

    private fun b64u(b: ByteArray): String = Base64.getUrlEncoder().withoutPadding().encodeToString(b)
}

/** Ссылка ss:// (формат SIP002) для официального приложения Shadowsocks. */
fun ssLink(k: ServerKey): String? {
    val ss = k.ss ?: return null
    val port = k.portFor(Transport.SS) ?: return null
    val userinfo = Base64.getUrlEncoder().withoutPadding()
        .encodeToString("${ss.method}:${ss.password}".toByteArray(Charsets.UTF_8))
    val tag = URLEncoder.encode(k.name.ifEmpty { "usatyi" }, "UTF-8").replace("+", "%20")
    return "ss://$userinfo@${k.host}:$port#$tag"
}

internal fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it.toInt() and 0xff) }

internal fun hexToBytes(s: String): ByteArray {
    require(s.length % 2 == 0) { "неверный hex" }
    return ByteArray(s.length / 2) { i -> s.substring(2 * i, 2 * i + 2).toInt(16).toByte() }
}

/** Криптография: ECDH P-256, HKDF-SHA256, HMAC-SHA256, ChaCha20-Poly1305 (стандартные примитивы JCA). */
internal object Crypto {
    private const val CURVE = "secp256r1"
    private val rng = SecureRandom()

    private val chachaEnc: ThreadLocal<Cipher> =
        ThreadLocal.withInitial { Cipher.getInstance("ChaCha20-Poly1305") }
    private val chachaDec: ThreadLocal<Cipher> =
        ThreadLocal.withInitial { Cipher.getInstance("ChaCha20-Poly1305") }

    private val params: ECParameterSpec by lazy {
        val ap = AlgorithmParameters.getInstance("EC")
        ap.init(ECGenParameterSpec(CURVE))
        ap.getParameterSpec(ECParameterSpec::class.java)
    }

    fun newKeyPair(): KeyPair {
        val g = KeyPairGenerator.getInstance("EC")
        g.initialize(ECGenParameterSpec(CURVE), rng)
        return g.generateKeyPair()
    }

    /** Несжатая точка: 0x04 ‖ X(32) ‖ Y(32). */
    fun encodePub(pub: PublicKey): ByteArray {
        val w = (pub as ECPublicKey).w
        val out = ByteArray(65)
        out[0] = 4
        System.arraycopy(fixed32(w.affineX), 0, out, 1, 32)
        System.arraycopy(fixed32(w.affineY), 0, out, 33, 32)
        return out
    }

    fun decodePub(enc: ByteArray): PublicKey {
        if (enc.size != 65 || enc[0].toInt() != 4) throw GeneralSecurityException("неверная точка")
        val x = BigInteger(1, enc.copyOfRange(1, 33))
        val y = BigInteger(1, enc.copyOfRange(33, 65))
        return KeyFactory.getInstance("EC").generatePublic(ECPublicKeySpec(ECPoint(x, y), params))
    }

    private fun fixed32(v: BigInteger): ByteArray {
        val raw = v.toByteArray()
        val out = ByteArray(32)
        val n = minOf(raw.size, 32)
        System.arraycopy(raw, raw.size - n, out, 32 - n, n)
        return out
    }

    fun ecdh(priv: PrivateKey, pub: PublicKey): ByteArray {
        val ka = KeyAgreement.getInstance("ECDH")
        ka.init(priv)
        ka.doPhase(pub, true)
        return ka.generateSecret()
    }

    fun hmac(key: ByteArray, vararg parts: ByteArray): ByteArray {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(key, "HmacSHA256"))
        for (p in parts) mac.update(p)
        return mac.doFinal()
    }

    /** Первые 16 байт HMAC-SHA256 по частям. */
    fun tag16(key: ByteArray, vararg parts: ByteArray): ByteArray = hmac(key, *parts).copyOf(16)

    /** HKDF по RFC 5869 с HMAC-SHA256. */
    fun hkdf(ikm: ByteArray, salt: ByteArray, info: ByteArray, length: Int): ByteArray {
        val prk = hmac(salt, ikm)
        val out = ByteArray(length)
        var t = ByteArray(0)
        var pos = 0
        var i = 1
        while (pos < length) {
            t = hmac(prk, t, info, byteArrayOf(i.toByte()))
            val n = minOf(t.size, length - pos)
            System.arraycopy(t, 0, out, pos, n)
            pos += n
            i++
        }
        return out
    }

    private fun nonce(ctr: Long): ByteArray {
        val n = ByteArray(12)
        for (i in 0 until 8) n[4 + i] = (ctr ushr (56 - 8 * i)).toByte()
        return n
    }

    fun seal(key: ByteArray, ctr: Long, aad: ByteArray, plain: ByteArray): ByteArray {
        val c = chachaEnc.get()
        c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "ChaCha20"), IvParameterSpec(nonce(ctr)))
        c.updateAAD(aad)
        return c.doFinal(plain)
    }

    /** Открытый текст или null, если тег не сошёлся. */
    fun open(
        key: ByteArray,
        ctr: Long,
        aad: ByteArray,
        input: ByteArray,
        off: Int = 0,
        len: Int = input.size,
    ): ByteArray? {
        return try {
            val c = chachaDec.get()
            c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "ChaCha20"), IvParameterSpec(nonce(ctr)))
            c.updateAAD(aad)
            c.doFinal(input, off, len)
        } catch (e: GeneralSecurityException) {
            null
        }
    }
}

private fun putU64(b: ByteArray, off: Int, v: Long) {
    for (i in 0 until 8) b[off + i] = (v ushr (56 - 8 * i)).toByte()
}

private fun getU64(b: ByteArray, off: Int): Long {
    var v = 0L
    for (i in 0 until 8) v = (v shl 8) or (b[off + i].toLong() and 0xffL)
    return v
}

/** Окно защиты от повторов на 64 пакета (как в сервере). */
class ReplayWindow {
    private var top = -1L
    private var bits = 0L

    @Synchronized
    fun ok(ctr: Long): Boolean {
        if (top < 0L || ctr > top) return true
        val d = top - ctr
        if (d >= 64L) return false
        return ((bits ushr d.toInt()) and 1L) == 0L
    }

    @Synchronized
    fun mark(ctr: Long) {
        if (top < 0L || ctr > top) {
            val shift = if (top < 0L) 64L else ctr - top
            bits = if (shift >= 64L) 0L else (bits shl shift.toInt())
            bits = bits or 1L
            top = ctr
        } else {
            val d = top - ctr
            if (d < 64L) bits = bits or (1L shl d.toInt())
        }
    }
}

/**
 * Сессия MTP/1-3 поверх готового канала [link]: рукопожатие HELLO/WELCOME и шифрование DATA.
 * encrypt() может вызываться из двух потоков, decrypt() — только из сетевого.
 */
class MtpClient(private val key: ServerKey, private val link: Link) {

    class Welcome(val sid: ByteArray, val ip: ByteArray, val dns: ByteArray, val mtu: Int)

    private val txLock = Any()
    private var sid = ByteArray(4)
    private var kTx = ByteArray(32)
    private var kRx = ByteArray(32)
    private var txCtr = 0L
    private var ready = false
    private val replay = ReplayWindow()

    /** Отправляет HELLO и ждёт WELCOME. Бросает исключение, если сервер отказал или молчит. */
    fun handshake(attempts: Int = 5, perAttemptMs: Int = 2000): Welcome {
        repeat(attempts) {
            val kp = Crypto.newKeyPair()
            val cpub = Crypto.encodePub(kp.public)
            val hello = ByteArray(Mtp.HELLO_LEN)
            hello[0] = Mtp.T_HELLO.toByte()
            System.arraycopy(cpub, 0, hello, 1, 65)
            System.arraycopy(key.uid, 0, hello, 66, 8)
            putU64(hello, 74, System.currentTimeMillis() / 1000L)
            val tag = Crypto.tag16(key.secret, Mtp.LBL_HELLO, hello.copyOfRange(0, 82))
            System.arraycopy(tag, 0, hello, 82, 16)
            link.send(hello)

            val deadline = System.currentTimeMillis() + perAttemptMs
            while (true) {
                val left = deadline - System.currentTimeMillis()
                if (left <= 0L) break
                val f = link.recv(left.toInt()) ?: break
                if (f.size == Mtp.REJECT_LEN && f[0].toInt() == Mtp.T_REJECT) {
                    val expect = Crypto.tag16(key.secret, Mtp.LBL_REJECT, cpub, f.copyOfRange(0, 2))
                    if (expect.contentEquals(f.copyOfRange(2, 18))) {
                        throw MtpRejected(f[1].toInt() and 0xff)
                    }
                } else if (f.size == Mtp.WELCOME_LEN && f[0].toInt() == Mtp.T_WELCOME) {
                    val w = acceptWelcome(f, cpub, kp.private)
                    if (w != null) return w
                }
            }
        }
        throw MtpTimeout()
    }

    /**
     * Рукопожатие с параметрами для способа. Для TCP первый WELCOME может прийти позже двух секунд
     * (мобильная сеть, помехи), поэтому даём больше времени. Этим же пользуются приложение и живые тесты.
     */
    fun handshakeFor(t: Transport): Welcome =
        if (t == Transport.MTP1) handshake() else handshake(attempts = 3, perAttemptMs = 6000)

    private fun acceptWelcome(buf: ByteArray, cpub: ByteArray, priv: PrivateKey): Welcome? {
        // WELCOME: [0x02][sid 4][ip 4][dns 4][mtu 2][spub 65][tag 16]
        val body = buf.copyOfRange(0, 80)
        val expect = Crypto.tag16(key.secret, Mtp.LBL_WELCOME, cpub, body)
        if (!expect.contentEquals(buf.copyOfRange(80, 96))) return null
        val sidNew = buf.copyOfRange(1, 5)
        val ip = buf.copyOfRange(5, 9)
        val dns = buf.copyOfRange(9, 13)
        val mtu = ((buf[13].toInt() and 0xff) shl 8) or (buf[14].toInt() and 0xff)
        val spub = buf.copyOfRange(15, 80)

        val shared = Crypto.ecdh(priv, Crypto.decodePub(spub))
        val okm = Crypto.hkdf(shared + key.secret, cpub + spub, Mtp.LBL_KEYS, 64)
        synchronized(txLock) {
            sid = sidNew
            kTx = okm.copyOfRange(0, 32) // клиент -> сервер
            kRx = okm.copyOfRange(32, 64) // сервер -> клиент
            txCtr = 0L
            ready = true
        }
        return Welcome(sidNew, ip, dns, mtu)
    }

    /** Шифрует пакет и возвращает готовый DATA-кадр: [0x04][sid][ctr][ChaCha20-Poly1305(kind ‖ payload)]. */
    fun encrypt(kind: Int, src: ByteArray, off: Int, len: Int): ByteArray {
        val hdr = ByteArray(Mtp.HDR_LEN)
        var ctr = 0L
        var txKey = ByteArray(0)
        synchronized(txLock) {
            check(ready) { "сессия не установлена" }
            ctr = txCtr++
            txKey = kTx
            hdr[0] = Mtp.T_DATA.toByte()
            System.arraycopy(sid, 0, hdr, 1, 4)
            putU64(hdr, 5, ctr)
        }
        val plain = ByteArray(1 + len)
        plain[0] = kind.toByte()
        System.arraycopy(src, off, plain, 1, len)
        val ct = Crypto.seal(txKey, ctr, hdr, plain)
        val out = ByteArray(Mtp.HDR_LEN + ct.size)
        System.arraycopy(hdr, 0, out, 0, Mtp.HDR_LEN)
        System.arraycopy(ct, 0, out, Mtp.HDR_LEN, ct.size)
        return out
    }

    fun keepalive(): ByteArray = encrypt(Mtp.K_KEEPALIVE, ByteArray(0), 0, 0)

    /** Расшифровывает DATA-кадр. Возвращает пару (вид, данные) или null, если кадр не годится. */
    fun decrypt(buf: ByteArray, len: Int): Pair<Int, ByteArray>? {
        if (len < Mtp.MIN_DATA_LEN || buf[0].toInt() != Mtp.T_DATA) return null
        for (i in 0 until 4) {
            if (buf[1 + i] != sid[i]) return null
        }
        val ctr = getU64(buf, 5)
        if (!replay.ok(ctr)) return null
        val hdr = buf.copyOfRange(0, Mtp.HDR_LEN)
        val pt = Crypto.open(kRx, ctr, hdr, buf, Mtp.HDR_LEN, len - Mtp.HDR_LEN) ?: return null
        replay.mark(ctr)
        if (pt.isEmpty()) return null
        return Pair(pt[0].toInt() and 0xff, pt.copyOfRange(1, pt.size))
    }
}
