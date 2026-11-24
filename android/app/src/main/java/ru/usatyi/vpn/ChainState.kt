package ru.usatyi.vpn

import java.util.concurrent.CopyOnWriteArraySet

/**
 * Живое состояние цепочки «AmneziaWG → SSH → MTP» для экрана подключения.
 * Пишет служба (TunnelService), читают экраны и уведомление.
 */
object ChainState {
    enum class Phase { OFF, STARTING, WAIT_VPN, RUNNING, FAILED }

    /** Что происходит сейчас: этап словами (для карточки статуса). */
    @Volatile var phase: Phase = Phase.OFF
        private set

    @Volatile var stage: String = ""
        private set

    @Volatile var error: String = ""
        private set

    /** Счётчики: AmneziaWG (первая ступень) и MTP (то, что видит сервер). */
    @Volatile var awgRx: Long = 0
        private set
    @Volatile var awgTx: Long = 0
        private set
    @Volatile var rx: Long = 0
        private set
    @Volatile var tx: Long = 0
        private set

    private val listeners = CopyOnWriteArraySet<(Unit) -> Unit>()

    fun addListener(l: (Unit) -> Unit) {
        listeners.add(l)
        l(Unit)
    }

    fun removeListener(l: (Unit) -> Unit) {
        listeners.remove(l)
    }

    fun stage(text: String) {
        stage = text
        phase = Phase.STARTING
        publish()
    }

    fun waitingVpn(text: String) {
        stage = text
        phase = Phase.WAIT_VPN
        publish()
    }

    fun running() {
        stage = ""
        error = ""
        phase = Phase.RUNNING
        publish()
    }

    fun fail(text: String) {
        stage = ""
        error = text
        phase = Phase.FAILED
        publish()
    }

    fun off() {
        phase = Phase.OFF
        stage = ""
        error = ""
        awgRx = 0
        awgTx = 0
        rx = 0
        tx = 0
        publish()
    }

    fun traffic(awgRx: Long, awgTx: Long, rx: Long, tx: Long) {
        this.awgRx = awgRx
        this.awgTx = awgTx
        this.rx = rx
        this.tx = tx
        publish()
    }

    private fun publish() {
        for (l in listeners) l(Unit)
    }
}
