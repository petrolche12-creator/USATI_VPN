package ru.usatyi.vpn

/**
 * Почему туннель остановился. Правило одно: записывается первая настоящая ошибка.
 *
 * Когда пара потоков туннеля падает, второй поток ломается уже из-за закрытия первого
 * (например, интерфейс закрыт и чтение даёт EBADF). Такие вторичные ошибки не должны
 * заменять причину. Если остановка уже начата (пользователем или после ошибки),
 * новые ошибки не записываются.
 */
class StopReason {
    @Volatile
    var stopping = false
        private set

    @Volatile
    var failure: String? = null
        private set

    /** Остановка без ошибки: пользователь нажал «Отключить» или систему отозвала VPN. */
    @Synchronized
    fun stop() {
        stopping = true
    }

    /** Ошибка. Записывается, только если остановка ещё не начата. */
    @Synchronized
    fun fail(message: String) {
        if (!stopping) {
            failure = message
            stopping = true
        }
    }

    /** Начало новой сессии. */
    @Synchronized
    fun reset() {
        stopping = false
        failure = null
    }
}
