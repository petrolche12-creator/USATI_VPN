package ru.usatyi.vpn

import android.content.Context
import java.io.File
import java.util.Locale

/**
 * Файл .conf из Amnezia («Поделиться») — первая ступень цепочки:
 *
 *   [.conf AmneziaWG] → [туннель AmneziaWG] → [SSH на свой сервер] → [MTP-VPN]
 *
 * Здесь только разбор, проверка и хранение файла. Подключение делает MainActivity/TunnelService.
 * Разбор нарочно строгий: если в файле нет ключей или адреса сервера, лучше сказать сразу,
 * чем показать «подключено» и не пустить никуда.
 */
data class AwgPeer(
    val publicKey: String,
    val endpoint: String,
    val allowedIPs: List<String>,
    val keepalive: Int,
) {
    val host: String get() = endpoint.substringBeforeLast(':', endpoint)
    val port: Int get() = endpoint.substringAfterLast(':', "51820").toIntOrNull() ?: 51820
}

data class AwgConfig(
    val name: String,
    val privateKey: String,
    val addresses: List<String>,
    val dns: List<String>,
    val mtu: Int,
    val peer: AwgPeer,
    val obf: Map<String, String>,
) {
    val kind: String get() = if (obf.isEmpty()) "WireGuard" else "AmneziaWG"

    /** Конфиг без параметров обфускации — для стандартной библиотеки WireGuard. */
    fun plainText(): String {
        val sb = StringBuilder()
        sb.append("[Interface]\n")
        sb.append("PrivateKey = ").append(privateKey).append('\n')
        if (addresses.isNotEmpty()) sb.append("Address = ").append(addresses.joinToString(", ")).append('\n')
        if (dns.isNotEmpty()) sb.append("DNS = ").append(dns.joinToString(", ")).append('\n')
        if (mtu > 0) sb.append("MTU = ").append(mtu).append('\n')
        sb.append("\n[Peer]\n")
        sb.append("PublicKey = ").append(peer.publicKey).append('\n')
        sb.append("AllowedIPs = ").append(
            if (peer.allowedIPs.isEmpty()) "0.0.0.0/0" else peer.allowedIPs.joinToString(", "),
        ).append('\n')
        sb.append("Endpoint = ").append(peer.endpoint).append('\n')
        sb.append("PersistentKeepalive = ").append(if (peer.keepalive > 0) peer.keepalive else 25).append('\n')
        return sb.toString()
    }
}

/** Ключи обфускации AmneziaWG: их понимает только сервер Amnezia, стандартная библиотека — нет. */
object Amnezia {
    private val OBF_KEYS = setOf(
        "jc", "jmin", "jmax", "s1", "s2", "s3", "s4",
        "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5", "j1", "j2", "j3",
        "itime", "headerprotectionkey", "contentpaddingaddition", "rekeyaftertime",
        "rekeytimeout", "rejectaftertime", "keepalivetimeout", "maxhandshakeattempts",
        "randomtrailers", "disablecookies",
    )

    fun parse(text: String, name: String): AwgConfig {
        var section = ""
        var priv = ""
        val addresses = mutableListOf<String>()
        val dns = mutableListOf<String>()
        var mtu = 0
        var pub = ""
        var endpoint = ""
        val allowed = mutableListOf<String>()
        var keepalive = 0
        val obf = LinkedHashMap<String, String>()

        for (raw in text.lineSequence()) {
            val line = raw.trim()
            if (line.isEmpty() || line.startsWith("#") || line.startsWith(";")) continue
            if (line.startsWith("[")) {
                section = line.trim('[', ']').lowercase(Locale.US)
                continue
            }
            val eq = line.indexOf('=')
            if (eq <= 0) continue
            val key = line.substring(0, eq).trim()
            val value = line.substring(eq + 1).trim()
            if (value.isEmpty()) continue
            when (section) {
                "interface" -> when (key.lowercase(Locale.US)) {
                    "privatekey" -> priv = value
                    "address" -> addresses += value.split(',').map { it.trim() }.filter { it.isNotEmpty() }
                    "dns" -> dns += value.split(',').map { it.trim() }.filter { it.isNotEmpty() }
                    "mtu" -> mtu = value.toIntOrNull() ?: 0
                    else -> if (key.lowercase(Locale.US) in OBF_KEYS) obf[key] = value
                }
                "peer" -> when (key.lowercase(Locale.US)) {
                    "publickey" -> pub = value
                    "endpoint" -> endpoint = value
                    "allowedips" -> allowed += value.split(',').map { it.trim() }.filter { it.isNotEmpty() }
                    "persistentkeepalive" -> keepalive = value.toIntOrNull() ?: 0
                }
            }
        }

        if (priv.isEmpty()) throw MtpException("в файле нет PrivateKey — это не конфиг WireGuard/Amnezia")
        if (pub.isEmpty()) throw MtpException("в файле нет PublicKey сервера (секция [Peer])")
        if (!endpoint.contains(':') || endpoint.substringBeforeLast(':').isEmpty()) {
            throw MtpException("в файле нет Endpoint в виде адрес:порт — некуда подключаться")
        }
        if (addresses.isEmpty()) throw MtpException("в файле нет Address — какой адрес у этого устройства в туннеле")
        return AwgConfig(
            name = name,
            privateKey = priv,
            addresses = addresses,
            dns = dns,
            mtu = if (mtu in 576..1500) mtu else 1420,
            peer = AwgPeer(pub, endpoint, allowed, keepalive),
            obf = obf,
        )
    }

    /** Короткое описание для экрана: что нашлось в файле. */
    fun summary(c: AwgConfig): String {
        val sb = StringBuilder()
        sb.append("Сервер Amnezia: ").append(c.peer.endpoint).append('\n')
        sb.append("Адрес устройства: ").append(c.addresses.joinToString(", ")).append('\n')
        if (c.dns.isNotEmpty()) sb.append("DNS: ").append(c.dns.joinToString(", ")).append('\n')
        sb.append("Тип: ").append(c.kind)
        if (c.obf.isNotEmpty()) {
            sb.append(" (обфускация: ").append(c.obf.keys.joinToString(", ")).append(')')
        }
        sb.append('\n').append("MTU: ").append(c.mtu)
        return sb.toString()
    }
}

/** Хранение загруженного .conf: filesDir/amnezia.conf + имя файла в настройках. */
object AmneziaStore {
    private const val PREFS = "usatyi_vpn"
    private const val K_NAME = "amnezia_name"

    private fun file(c: Context): File = File(c.filesDir, "amnezia.conf")

    /** Разбирает и сохраняет .conf, если он корректен. Возвращает разобранный конфиг. */
    fun save(c: Context, text: String, name: String): AwgConfig {
        val cfg = Amnezia.parse(text, name) // сначала проверка, потом запись
        val f = file(c)
        f.writeText(text)
        f.setReadable(false, false)
        f.setReadable(true, true)
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(K_NAME, name).apply()
        return cfg
    }

    /** Загруженный конфиг или null. */
    fun load(c: Context): AwgConfig? {
        val text = rawText(c).takeIf { it.isNotEmpty() } ?: return null
        val name = displayName(c)
        return runCatching { Amnezia.parse(text, name) }.getOrNull()
    }

    /** Текст загруженного .conf (без разбора). Пустая строка, если файла нет. */
    fun rawText(c: Context): String {
        val f = file(c)
        if (!f.isFile) return ""
        return runCatching { f.readText() }.getOrDefault("")
    }

    /** Когда файл загружен (для экрана). */
    fun savedAt(c: Context): Long = if (file(c).isFile) file(c).lastModified() else 0L

    fun displayName(c: Context): String =
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(K_NAME, "")?.ifEmpty { "amnezia" } ?: "amnezia"

    fun forget(c: Context) {
        file(c).delete()
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().remove(K_NAME).apply()
    }

    /** «Весь трафик компьютера в туннель Amnezia» — как на Windows. */
    fun asVpn(c: Context): Boolean = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getBoolean("amnezia_as_vpn", false)

    fun setAsVpn(c: Context, v: Boolean) {
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putBoolean("amnezia_as_vpn", v).apply()
    }
}
