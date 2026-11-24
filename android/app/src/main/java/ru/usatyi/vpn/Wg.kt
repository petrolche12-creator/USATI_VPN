package ru.usatyi.vpn

import android.content.Context
import com.wireguard.android.backend.GoBackend
import com.wireguard.android.backend.Tunnel
import com.wireguard.config.Config
import java.io.BufferedReader
import java.io.StringReader

/** Текст конфигурации WireGuard (формат wg-quick) для клиента из ключа. */
object WgConfig {
    fun build(key: ServerKey, routing: Routing, apps: Set<String>, nonRu: List<String>): String {
        val w = requireNotNull(key.wg) { "в ключе нет параметров WireGuard" }
        val port = requireNotNull(key.portFor(Transport.WG)) { "в ключе нет порта WireGuard" }
        val allowed = if (routing == Routing.NOT_RU) {
            (nonRu + "::/0").joinToString(", ")
        } else {
            "0.0.0.0/0, ::/0"
        }
        val sb = StringBuilder()
        sb.append("[Interface]\n")
        sb.append("PrivateKey = ").append(w.privateKey).append('\n')
        sb.append("Address = ").append(w.address).append('\n')
        sb.append("DNS = ").append(w.dns).append('\n')
        sb.append("MTU = 1380\n")
        if (routing == Routing.APPS && apps.isNotEmpty()) {
            sb.append("IncludedApplications = ").append(apps.joinToString(", ")).append('\n')
        }
        sb.append("\n[Peer]\n")
        sb.append("PublicKey = ").append(w.serverPublicKey).append('\n')
        sb.append("AllowedIPs = ").append(allowed).append('\n')
        sb.append("Endpoint = ").append(key.host).append(':').append(port).append('\n')
        sb.append("PersistentKeepalive = 20\n")
        return sb.toString()
    }
}

/** Туннель WireGuard на встроенной библиотеке WireGuard for Android (com.wireguard.android:tunnel). */
object WgController {
    private var backend: GoBackend? = null
    private var lastConfig: Config? = null

    private val tunnel = object : Tunnel {
        override fun getName(): String = "usatyi"
        override fun onStateChange(newState: Tunnel.State) {}
    }

    /** Поднимает туннель. Вызывать не из главного потока. Бросает исключение при ошибке. */
    @Synchronized
    fun up(context: Context, configText: String) {
        val b = backend ?: GoBackend(context.applicationContext).also { backend = it }
        val cfg = Config.parse(BufferedReader(StringReader(configText)))
        b.setState(tunnel, Tunnel.State.UP, cfg)
        lastConfig = cfg
    }

    /** Опускает туннель. Вызывать не из главного потока. */
    @Synchronized
    fun down() {
        val b = backend ?: return
        val cfg = lastConfig ?: return
        b.setState(tunnel, Tunnel.State.DOWN, cfg)
    }

    /** Поднят ли туннель по данным библиотеки. */
    @Synchronized
    fun isUp(): Boolean {
        val b = backend ?: return false
        return b.getState(tunnel) == Tunnel.State.UP
    }

    /** Байты за текущий сеанс: получено и отправлено; null, если туннеля не было. */
    @Synchronized
    fun traffic(): Pair<Long, Long>? {
        val b = backend ?: return null
        val st = b.getStatistics(tunnel) ?: return null
        return Pair(st.totalRx(), st.totalTx())
    }
}
