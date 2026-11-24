package ru.usatyi.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Режимы: какой способ и маршрутизация пойдут в туннель для ключа. */
class ModesTest {

    private val all: (Transport) -> Boolean = { true }

    private fun without(vararg gone: Transport): (Transport) -> Boolean = { it !in gone }

    private fun ok(c: Choice): Choice.Ok {
        assertTrue("ожидали успешный выбор, получили $c", c is Choice.Ok)
        return c as Choice.Ok
    }

    @Test
    fun defaultKeepsKeyChoice() {
        // по умолчанию: обычный режим, ручной протокол выключен — работает то, что в ключе
        val c = ok(Modes.choose(AppModes(Mode.NORMAL, false, ProtoChoice.AUTO), Transport.MTP2, Routing.NOT_RU, all))
        assertEquals(Transport.MTP2, c.transport)
        assertEquals(Routing.NOT_RU, c.routing)
        assertEquals(Mode.NORMAL, c.mode)
    }

    @Test
    fun modeWorksWithoutManualProtocol() {
        // режим меняет работу сразу, даже когда ручной выбор протокола выключен
        val game = ok(Modes.choose(AppModes(Mode.GAME, false, ProtoChoice.WG), Transport.MTP2, Routing.NOT_RU, all))
        assertEquals(Transport.MTP1, game.transport) // WG из настроек не учитывается: ручной выбор выключен
        assertEquals(Mode.GAME, game.mode)
        val anon = ok(Modes.choose(AppModes(Mode.ANON, false, ProtoChoice.AUTO), Transport.MTP1, Routing.NOT_RU, all))
        assertEquals(Transport.MTP3, anon.transport)
        assertEquals(Routing.ALL, anon.routing)
    }

    @Test
    fun gameUsesUdpThenWireGuard() {
        val game = AppModes(Mode.GAME, false, ProtoChoice.AUTO)
        assertEquals(Transport.MTP1, ok(Modes.choose(game, Transport.MTP2, Routing.NOT_RU, all)).transport)
        assertEquals(Transport.WG, ok(Modes.choose(game, Transport.MTP2, Routing.NOT_RU, without(Transport.MTP1))).transport)
        assertTrue(Modes.choose(game, Transport.MTP2, Routing.NOT_RU, without(Transport.MTP1, Transport.WG)) is Choice.Error)
    }

    @Test
    fun gameForcesNotRuEvenIfKeySaysAll() {
        val c = ok(Modes.choose(AppModes(Mode.GAME, false, ProtoChoice.AUTO), Transport.MTP2, Routing.ALL, all))
        assertEquals(Routing.NOT_RU, c.routing)
        assertEquals(Mode.GAME, c.mode)
    }

    @Test
    fun anonForcesMaskAndAllTraffic() {
        val anon = AppModes(Mode.ANON, false, ProtoChoice.AUTO)
        val c = ok(Modes.choose(anon, Transport.MTP1, Routing.NOT_RU, all))
        assertEquals(Transport.MTP3, c.transport)
        assertEquals(Routing.ALL, c.routing)
        assertEquals(Mode.ANON, c.mode)
        // без MTP/3 тихой подмены нет: ошибка
        assertTrue(Modes.choose(anon, Transport.MTP1, Routing.NOT_RU, without(Transport.MTP3)) is Choice.Error)
    }

    @Test
    fun manualProtocolNeedsTheSwitch() {
        val off = ok(Modes.choose(AppModes(Mode.NORMAL, false, ProtoChoice.WG), Transport.MTP2, Routing.NOT_RU, all))
        assertEquals(Transport.MTP2, off.transport)
        val on = ok(Modes.choose(AppModes(Mode.NORMAL, true, ProtoChoice.WG), Transport.MTP2, Routing.NOT_RU, all))
        assertEquals(Transport.WG, on.transport)
        // протокола нет в ключе — ошибка, а не тихая подмена
        assertTrue(Modes.choose(AppModes(Mode.NORMAL, true, ProtoChoice.WG), Transport.MTP2, Routing.NOT_RU, without(Transport.WG)) is Choice.Error)
    }

    @Test
    fun manualProtocolInAnonKeepsAllTraffic() {
        // в анонимном режиме выбранный вручную протокол работает, маршрутизация остаётся «весь трафик»
        val c = ok(Modes.choose(AppModes(Mode.ANON, true, ProtoChoice.MTP1), Transport.MTP2, Routing.NOT_RU, all))
        assertEquals(Transport.MTP1, c.transport)
        assertEquals(Routing.ALL, c.routing)
    }
}
