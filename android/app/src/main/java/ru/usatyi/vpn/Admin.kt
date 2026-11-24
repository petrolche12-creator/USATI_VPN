package ru.usatyi.vpn

import com.jcraft.jsch.ChannelExec
import com.jcraft.jsch.JSch
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.util.Locale
import org.json.JSONObject

/** Свой сервер: как к нему подключаться по SSH и какой UDP-порт слушает VPN. */
data class Owner(
    val host: String,
    val sshPort: Int,
    val sshUser: String,
    val sshPass: String,
    val vpnPort: Int,
    val isRoot: Boolean,
    val hostFp: String,
)

class SshResult(val code: Int, val out: String, val err: String, val fingerprint: String)

/**
 * SSH через JSch. Отпечаток ключа сервера запоминается при первом входе (TOFU)
 * и проверяется при всех следующих подключениях.
 */
object Ssh {
    private const val NL: Byte = 10

    fun exec(
        cfg: Owner,
        command: String,
        stdin: ByteArray? = null,
        onLine: (String) -> Unit = {},
    ): SshResult {
        val jsch = JSch()
        val session = jsch.getSession(cfg.sshUser, cfg.host, cfg.sshPort)
        session.setPassword(cfg.sshPass)
        session.setConfig("StrictHostKeyChecking", "no")
        session.setConfig("PreferredAuthentications", "password,keyboard-interactive")
        session.connect(20_000)
        try {
            val fp = session.hostKey.getFingerPrint(jsch)
            if (cfg.hostFp.isNotEmpty() && cfg.hostFp != fp) {
                throw IOException(
                    "ключ сервера изменился. Если сервер переустанавливали, нажми «Забыть сервер» и установи заново",
                )
            }
            val ch = session.openChannel("exec") as ChannelExec
            ch.setCommand(command)
            val err = ByteArrayOutputStream()
            ch.setErrStream(err)
            val input = ch.inputStream
            ch.connect(20_000)
            val os = ch.outputStream
            if (stdin != null) os.write(stdin)
            os.close()

            val out = ByteArrayOutputStream()
            val line = ByteArrayOutputStream()
            val buf = ByteArray(4096)
            while (true) {
                val n = input.read(buf)
                if (n < 0) break
                out.write(buf, 0, n)
                for (i in 0 until n) {
                    if (buf[i] == NL) {
                        onLine(line.toString("UTF-8"))
                        line.reset()
                    } else {
                        line.write(buf[i].toInt())
                    }
                }
            }
            if (line.size() > 0) onLine(line.toString("UTF-8"))

            val deadline = System.currentTimeMillis() + 15 * 60_000L
            while (!ch.isClosed && System.currentTimeMillis() < deadline) Thread.sleep(50)
            val code = ch.exitStatus
            ch.disconnect()
            return SshResult(code, out.toString("UTF-8"), err.toString("UTF-8"), fp)
        } finally {
            session.disconnect()
        }
    }
}

/** Управление сервером по SSH: установка и запуск mtp.py с командами add/list/set/delete. */
object Admin {
    private const val MTP = "/opt/mtp/mtp.py"
    private const val SETUP = "\$HOME/usatyi-setup"

    /** Экранирование аргумента для shell: одинарные кавычки. */
    fun q(s: String): String = "'" + s.replace("'", "'\\''") + "'"

    private fun wrap(cfg: Owner, cmd: String): String =
        if (cfg.isRoot) cmd else "sudo -S -p '' $cmd"

    /** Пароль уходит только через stdin (для sudo -S), в командной строке его нет. */
    private fun stdinFor(cfg: Owner): ByteArray? =
        if (cfg.isRoot) null else (cfg.sshPass + "\n").toByteArray(Charsets.UTF_8)

    private fun errorText(r: SshResult): String {
        val lines = r.err.lines().map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("[sudo]") }
        return if (lines.isEmpty()) "код выхода ${r.code}" else lines.takeLast(5).joinToString("\n")
    }

    /** Запускает mtp.py на сервере и возвращает stdout. При ошибке бросает IOException. */
    fun run(cfg: Owner, args: String): String {
        // Явно системный Python: под sudo PATH может указывать на /usr/local/bin/python3 без cryptography.
        val r = Ssh.exec(cfg, wrap(cfg, "/usr/bin/python3 $MTP $args"), stdinFor(cfg))
        if (r.code != 0) throw IOException(errorText(r))
        return r.out
    }

    private fun json(out: String): JSONObject {
        val s = out.indexOf('{')
        val e = out.lastIndexOf('}')
        if (s < 0 || e < s) throw IOException("неожиданный ответ сервера")
        return JSONObject(out.substring(s, e + 1))
    }

    fun list(cfg: Owner): JSONObject = json(run(cfg, "list --json"))

    fun add(cfg: Owner, name: String, days: Int, limitGb: Double): JSONObject {
        val limit = String.format(Locale.US, "%.4f", limitGb)
        return json(run(cfg, "add --name=${q(name)} --days=$days --limit-gb=$limit --json"))
    }

    fun show(cfg: Owner, name: String): JSONObject = json(run(cfg, "show ${q(name)} --json"))

    fun setEnabled(cfg: Owner, name: String, enabled: Boolean) {
        run(cfg, "set ${q(name)} ${if (enabled) "--enable" else "--disable"} --json")
    }

    fun resetTraffic(cfg: Owner, name: String) {
        run(cfg, "set ${q(name)} --reset --json")
    }

    fun delete(cfg: Owner, name: String) {
        run(cfg, "delete ${q(name)} --json")
    }

    /**
     * Установка: проверяет вход, загружает mtp.py и install.sh через stdin SSH,
     * запускает install.sh. Возвращает настройки сервера, которые надо сохранить на телефоне.
     */
    fun install(form: Owner, mtpPy: ByteArray, installSh: ByteArray, onLine: (String) -> Unit): Owner {
        onLine("SSH: вхожу на ${form.host}…")
        val probe = Ssh.exec(form, "id -u")
        if (probe.code != 0) throw IOException("вход по SSH не удался: ${errorText(probe)}")
        val isRoot = probe.out.trim() == "0"
        val fp = if (form.hostFp.isEmpty()) probe.fingerprint else form.hostFp
        val cfg = form.copy(isRoot = isRoot, hostFp = fp)
        onLine(if (isRoot) "вход под root" else "вход под ${cfg.sshUser}, использую sudo")

        val up1 = Ssh.exec(cfg, "mkdir -p $SETUP && cat > $SETUP/mtp.py", mtpPy)
        if (up1.code != 0) throw IOException("загрузка mtp.py: ${errorText(up1)}")
        val up2 = Ssh.exec(cfg, "cat > $SETUP/install.sh", installSh)
        if (up2.code != 0) throw IOException("загрузка install.sh: ${errorText(up2)}")
        onLine("файлы загружены, запускаю установку…")

        val r = Ssh.exec(
            cfg,
            wrap(cfg, "bash $SETUP/install.sh ${q(cfg.host)} ${cfg.vpnPort}"),
            stdinFor(cfg),
            onLine,
        )
        try {
            Ssh.exec(cfg, "rm -rf $SETUP")
        } catch (e: Exception) {
            // временные файлы не критичны
        }
        if (r.code != 0) throw IOException("установка завершилась с ошибкой: ${errorText(r)}")
        return cfg
    }
}
