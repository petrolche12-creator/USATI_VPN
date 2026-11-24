package ru.usatyi.vpn

import com.wireguard.config.Config
import java.io.BufferedReader
import java.io.StringReader
import java.util.Base64
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Конфиг WireGuard проверяем настоящим парсером библиотеки: если он не примет текст, туннель не поднимется. */
class WgConfigTest {

    private fun key(): ServerKey = ServerKey(
        name = "Вася",
        host = "203.0.113.5",
        uid = ByteArray(8) { 1 },
        secret = ByteArray(32) { 2 },
        ports = mapOf("wg" to 51820),
        wg = WgParams(
            Base64.getEncoder().encodeToString(ByteArray(32) { 7 }),
            Base64.getEncoder().encodeToString(ByteArray(32) { 9 }),
            "10.9.0.2/32",
            "1.1.1.1",
        ),
        ss = null,
    )

    private fun parse(text: String): Config = Config.parse(BufferedReader(StringReader(text)))

    @Test
    fun allTrafficConfigParsesAndUsesServerEndpoint() {
        val cfg = parse(WgConfig.build(key(), Routing.ALL, emptySet(), emptyList()))
        assertEquals(1, cfg.getPeers().size)
        val peer = cfg.getPeers()[0]
        assertTrue(peer.getAllowedIps().any { it.toString() == "0.0.0.0/0" })
        assertEquals("203.0.113.5:51820", peer.getEndpoint().get().toString())
    }

    @Test
    fun notRuConfigRoutesOnlyListedNetworksPlusIpv6() {
        val nets = listOf("1.0.0.0/8", "2.0.0.0/11")
        val cfg = parse(WgConfig.build(key(), Routing.NOT_RU, emptySet(), nets))
        // две IPv4-сети и ::/0
        assertEquals(3, cfg.getPeers()[0].getAllowedIps().size)
    }

    @Test
    fun appsModeListsIncludedApplications() {
        val cfg = parse(WgConfig.build(key(), Routing.APPS, setOf("org.example.a", "org.example.b"), emptyList()))
        assertEquals(setOf("org.example.a", "org.example.b"), cfg.getInterface().getIncludedApplications())
    }
}
