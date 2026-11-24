package ru.usatyi.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Список ключей на телефоне: настройки каждого ключа сохраняются и читаются обратно. */
class KeyStoreTest {

    private fun keyText(): String = KeyCodec.encode(
        ServerKey(
            name = "Вася",
            host = "203.0.113.5",
            uid = ByteArray(8) { 1 },
            secret = ByteArray(32) { 2 },
            ports = mapOf("udp" to 443, "tcp" to 8443),
            wg = null,
            ss = null,
        ),
    )

    @Test
    fun roundTripKeepsEachKeysSettings() {
        val k = SavedKey(
            id = "0101010101010101",
            text = keyText(),
            alias = "Друг",
            transport = Transport.MTP2,
            routing = Routing.APPS,
            apps = setOf("org.example.b", "org.example.a"),
        )
        val back = KeyStore.fromJson(KeyStore.toJson(listOf(k)))
        assertEquals(1, back.size)
        assertEquals("Друг", back[0].alias)
        assertEquals("Друг", back[0].name)
        assertEquals(Transport.MTP2, back[0].transport)
        assertEquals(Routing.APPS, back[0].routing)
        assertEquals(setOf("org.example.a", "org.example.b"), back[0].apps)
    }

    @Test
    fun nameFallsBackToKeyNameWhenNoAlias() {
        val k = SavedKey("0101010101010101", keyText(), "", Transport.MTP1, Routing.ALL, emptySet())
        assertEquals("Вася", k.name)
    }

    @Test
    fun brokenEntriesAreSkipped() {
        val raw = "[{\"id\":\"x\",\"text\":\"не ключ\"},{\"id\":\"y\"}]"
        assertTrue(KeyStore.fromJson(raw).isEmpty())
    }

    @Test
    fun defaultTransportPrefersOwnUdpProtocol() {
        val key = KeyCodec.decode(keyText())
        assertEquals(Transport.MTP1, KeyStore.defaultTransport(key))
    }
}
