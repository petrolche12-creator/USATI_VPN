package ru.usatyi.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class StopReasonTest {

    /** Настоящая причина: сервер закрыл TCP-соединение. Потом интерфейс закрывается и чтение даёт EBADF. */
    @Test
    fun firstErrorIsKeptWhenInterfaceCloseFollows() {
        val s = StopReason()
        s.fail("связь с сервером прервалась: соединение закрыто сервером")
        s.fail("ошибка туннеля: failed EBADF (Bad file descriptor)")
        assertEquals("связь с сервером прервалась: соединение закрыто сервером", s.failure)
        assertTrue(s.stopping)
    }

    /** Закрытие интерфейса после «Отключить» не считается ошибкой. */
    @Test
    fun errorAfterUserStopIsIgnored() {
        val s = StopReason()
        s.stop()
        s.fail("ошибка туннеля: failed EBADF (Bad file descriptor)")
        assertNull(s.failure)
        assertTrue(s.stopping)
    }

    @Test
    fun resetStartsNewSession() {
        val s = StopReason()
        s.fail("сервер молчит больше 90 секунд")
        s.reset()
        assertFalse(s.stopping)
        assertNull(s.failure)
    }
}
