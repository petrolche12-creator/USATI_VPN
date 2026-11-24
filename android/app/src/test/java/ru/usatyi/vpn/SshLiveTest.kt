package ru.usatyi.vpn

import java.io.IOException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Assume.assumeTrue
import org.junit.Test

/**
 * Живая проверка SSH-слоя против настоящего sshd (OpenSSH). Запуск:
 *   ./gradlew testDebugUnitTest -Pmtp.test.ssh="host|port|user|password"
 * Проверяет: вход (JSch и современный OpenSSH), exec, загрузку через stdin,
 * sudo -S с паролем из stdin и защиту от подмены отпечатка ключа сервера.
 * Пользователь не должен быть root. Без параметра тест пропускается.
 */
class SshLiveTest {

    private fun configFromProperty(): Owner? {
        val raw = System.getProperty("mtp.test.ssh")
        if (raw.isNullOrBlank()) return null
        val p = raw.split("|")
        require(p.size == 4) { "формат: host|port|user|password" }
        return Owner(
            host = p[0],
            sshPort = p[1].toInt(),
            sshUser = p[2],
            sshPass = p[3],
            vpnPort = 443,
            isRoot = p[2] == "root",
            hostFp = "",
        )
    }

    @Test
    fun loginExecUploadSudoAndHostKeyPinning() {
        val cfg = configFromProperty()
        assumeTrue("задай -Pmtp.test.ssh=host|port|user|password", cfg != null)
        val c = cfg!!

        val probe = Ssh.exec(c, "id -u")
        assertEquals("id -u: код выхода", 0, probe.code)
        val uid = probe.out.trim().toInt()
        assertFalse("тестовый пользователь не должен быть root", uid == 0)
        assertTrue("отпечаток ключа сервера пустой", probe.fingerprint.isNotEmpty())

        // загрузка файла через stdin, так же как Admin.install загружает mtp.py
        val payload = "привет, mtp/1\nвторая строка\n".toByteArray(Charsets.UTF_8)
        val up = Ssh.exec(c, "mkdir -p \$HOME/usatyi-test && cat > \$HOME/usatyi-test/blob.bin", payload)
        assertEquals("загрузка: код выхода", 0, up.code)
        val back = Ssh.exec(c, "cat \$HOME/usatyi-test/blob.bin")
        assertEquals("файл не совпал после загрузки", String(payload, Charsets.UTF_8), back.out)

        // sudo -S: пароль приходит только через stdin
        val sudo = Ssh.exec(c, "sudo -S -p '' id -u", (c.sshPass + "\n").toByteArray(Charsets.UTF_8))
        assertEquals("sudo: код выхода", 0, sudo.code)
        assertEquals("sudo должен дать uid 0", "0", sudo.out.trim())

        // TOFU: если сохранённый отпечаток не совпадает, вход должен блокироваться
        val wrong = c.copy(hostFp = "MD5:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff")
        try {
            Ssh.exec(wrong, "id -u")
            fail("подмена отпечатка не была замечена")
        } catch (e: IOException) {
            assertTrue("неожиданная ошибка: ${e.message}", e.message.orEmpty().contains("ключ сервера изменился"))
        }

        Ssh.exec(c, "rm -rf \$HOME/usatyi-test")
    }
}
