package ru.usatyi.vpn

import android.content.Context

/**
 * Режимы работы. Режим действует всегда: это главный выбор пользователя.
 * GAME — минимальный пинг и максимальная скорость: UDP без TCP-обёртки.
 * NORMAL — баланс: протокол и маршрутизация из ключа, как раньше.
 * ANON — максимальная скрытность: MTP/3 (маскировка под TLS) и весь трафик через VPN.
 */
enum class Mode(val label: String, val hint: String) {
    GAME(
        "Игровой",
        "минимальный пинг: UDP без маскировки (MTP/1, иначе WireGuard), трафик к российским адресам идёт напрямую, мимо VPN — пинг до них как без VPN",
    ),
    NORMAL(
        "Обычный",
        "баланс скорости и защиты: протокол и маршрутизация берутся из ключа, как раньше",
    ),
    ANON(
        "Максимально анонимный",
        "маскировка под HTTPS (MTP/3), весь трафик через VPN, скорость ниже. Это не абсолютная анонимность: сервер видит трафик",
    ),
}

/** Протокол, выбранный вручную. AUTO — протокол выбирает режим. Учитывается, только когда ручной выбор включён. */
enum class ProtoChoice(val label: String, val transport: Transport?) {
    AUTO("Авто (по режиму)", null),
    MTP1("MTP/1 · UDP", Transport.MTP1),
    MTP2("MTP/2 · TCP", Transport.MTP2),
    MTP3("MTP/3 · маскировка", Transport.MTP3),
    WG("WireGuard", Transport.WG),
}

/** Настройки: режим (всегда действует), ручной выбор протокола (выключен по умолчанию) и сам протокол. */
data class AppModes(val mode: Mode, val manual: Boolean, val proto: ProtoChoice)

/** Итог выбора для ключа: какой способ и маршрутизация пойдут в туннель. */
sealed class Choice {
    data class Ok(val transport: Transport, val routing: Routing, val mode: Mode) : Choice()
    data class Error(val message: String) : Choice()
}

object Modes {
    private const val PREFS = "usatyi_vpn"
    private const val K_MODE = "modes_mode"
    private const val K_MANUAL = "modes_manual"
    private const val K_PROTO = "modes_proto"

    fun load(c: Context): AppModes {
        val p = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val mode = runCatching { Mode.valueOf(p.getString(K_MODE, "") ?: "") }.getOrDefault(Mode.NORMAL)
        val proto = runCatching { ProtoChoice.valueOf(p.getString(K_PROTO, "") ?: "") }.getOrDefault(ProtoChoice.AUTO)
        return AppModes(mode, p.getBoolean(K_MANUAL, false), proto)
    }

    fun save(c: Context, m: AppModes) {
        c.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
            .putString(K_MODE, m.mode.name)
            .putBoolean(K_MANUAL, m.manual)
            .putString(K_PROTO, m.proto.name)
            .apply()
    }

    /**
     * Решает, с каким способом и маршрутизацией подключать ключ. Сам ключ не меняется.
     * Если нужного способа в ключе нет, возвращается ошибка: протокол тихо не подменяется.
     */
    fun choose(m: AppModes, keyTransport: Transport, keyRouting: Routing, supports: (Transport) -> Boolean): Choice {
        // Игровой режим уводит российские сети мимо VPN (пинг до них как без VPN),
        // анонимный собирает весь трафик в туннель — так же, как в Windows-версии.
        val routing = when (m.mode) {
            Mode.ANON -> Routing.ALL
            Mode.GAME -> Routing.NOT_RU
            Mode.NORMAL -> keyRouting
        }
        val proto = if (m.manual) m.proto else ProtoChoice.AUTO
        proto.transport?.let { t ->
            return if (supports(t)) {
                Choice.Ok(t, routing, m.mode)
            } else {
                Choice.Error("В ключе нет параметров для «${t.label}». Выберите другой протокол или другой ключ")
            }
        }
        return when (m.mode) {
            Mode.GAME -> {
                val t = listOf(Transport.MTP1, Transport.WG).firstOrNull(supports)
                    ?: return Choice.Error("Для игрового режима нужен MTP/1 или WireGuard, а в ключе их нет. Выберите обычный режим или другой ключ")
                Choice.Ok(t, routing, m.mode)
            }
            Mode.ANON -> if (supports(Transport.MTP3)) {
                Choice.Ok(Transport.MTP3, routing, m.mode)
            } else {
                Choice.Error("Для максимально анонимного режима нужен MTP/3 (маскировка), а в ключе его нет. Выберите обычный режим или другой ключ")
            }
            Mode.NORMAL -> Choice.Ok(keyTransport, routing, m.mode)
        }
    }
}
