package ru.usatyi.vpn

import java.util.concurrent.CopyOnWriteArraySet

/**
 * Состояние VPN для экрана. Служба и WireGuard пишут сюда, экран читает.
 * Здесь же видно, какой ключ сейчас поднят и каким способом.
 */
object VpnState {
    enum class Phase { IDLE, CONNECTING, CONNECTED, ERROR }

    /** Что именно поднято: ключ, адрес сервера, способ и режим маршрутизации. */
    data class Session(
        val keyId: String,
        val keyName: String,
        val host: String,
        val transport: Transport,
        val routing: Routing,
        val mode: Mode = Mode.NORMAL,
    )

    /**
     * Снимок состояния. rx и tx — байты за сеанс на телефоне.
     * used, limit, expires — данные сервера (только при подключении по MTP); null — неизвестно.
     */
    data class Snapshot(
        val phase: Phase = Phase.IDLE,
        val message: String = "Не подключено",
        val session: Session? = null,
        val startedAt: Long = 0L,
        val rx: Long = 0L,
        val tx: Long = 0L,
        val used: Long? = null,
        val limit: Long? = null,
        val expires: Long? = null,
    )

    @Volatile
    var current: Snapshot = Snapshot()
        private set

    private val listeners = CopyOnWriteArraySet<(Snapshot) -> Unit>()

    fun addListener(l: (Snapshot) -> Unit) {
        listeners.add(l)
        l(current)
    }

    fun removeListener(l: (Snapshot) -> Unit) {
        listeners.remove(l)
    }

    @Synchronized
    fun connecting(session: Session, message: String) {
        publish(Snapshot(Phase.CONNECTING, message, session))
    }

    @Synchronized
    fun connected(session: Session) {
        publish(Snapshot(Phase.CONNECTED, "Подключено", session, startedAt = System.currentTimeMillis()))
    }

    @Synchronized
    fun traffic(rx: Long, tx: Long) {
        publish(current.copy(rx = rx, tx = tx))
    }

    @Synchronized
    fun server(used: Long?, limit: Long?, expires: Long?) {
        publish(current.copy(used = used, limit = limit, expires = expires))
    }

    @Synchronized
    fun failed(message: String) {
        publish(current.copy(phase = Phase.ERROR, message = message))
    }

    @Synchronized
    fun idle(message: String = "Не подключено") {
        publish(Snapshot(Phase.IDLE, message))
    }

    private fun publish(s: Snapshot) {
        current = s
        for (l in listeners) l(s)
    }
}
