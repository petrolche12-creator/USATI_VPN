package ru.usatyi.vpn

import android.content.Context
import android.net.LocalSocket
import android.net.LocalSocketAddress
import android.util.Log
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.SocketTimeoutException
import java.util.concurrent.TimeUnit
import org.json.JSONObject

/**
 * Отдельный процесс ядра и связь с ним.
 *
 * Почему отдельный процесс:
 *   • AmneziaWG-туннель живёт в своём процессе со своим сетевым стеком, и этот процесс
 *     исключён из системного VPN (addDisallowedApplication), поэтому его пакеты не попадают
 *     в наш же VPN — никакой петли и никаких protect() из Go;
 *   • если ядро упадёт, VPN-служба это увидит и честно покажет ошибку, а не зависнет.
 *
 * Общение — локальный сокет Android (abstract namespace): [тип 1 байт][длина 4 байта][данные].
 * Формат описан в mobile/core/ipc.go, типы кадров — ниже.
 */
object CoreProcess {
    private const val TAG = "usatyi-core"

    /**
     * Исполняемый файл ядра. Он упакован как нативная библиотека (jniLibs/.../libusatyi_core.so),
     * поэтому система извлекает его в nativeLibraryDir, откуда файл можно запустить на любом
     * Android (начиная с 10 система запрещает запускать файлы из каталога данных приложения).
     */
    private fun binary(ctx: Context): File = File(ctx.applicationInfo.nativeLibraryDir, "libusatyi_core.so")

    fun available(ctx: Context): Boolean = binary(ctx).isFile

    /** Что сказать пользователю, если ядра нет в сборке. */
    fun missingReason(ctx: Context): String =
        if (available(ctx)) "" else "В этой сборке нет ядра цепочки Amnezia (mobile/build-android.sh). Скачайте сборку с ядром."

    /**
     * Путь к сокету ядра. Файловый (не абстрактный) — так его видит и наш процесс,
     * и процесс ядра, без ограничений SELinux на абстрактные сокеты в чужих каталогах.
     */
    private fun socketPath(ctx: Context) = File(ctx.cacheDir, "core.sock").absolutePath

    /**
     * Запускает ядро с параметрами подключения и возвращает открытую связь.
     * Тяжёлая операция (запуск процесса, рукопожатие AmneziaWG и MTP): вызывать из фонового потока.
     */
    fun spawn(ctx: Context, paramsJSON: String): Link {
        val bin = binary(ctx)
        if (!bin.isFile) throw IOException(missingReason(ctx))
        if (!bin.canExecute() && !bin.setExecutable(true, true)) {
            throw IOException("система не разрешила запустить ядро: ${bin.absolutePath}")
        }

        // Параметры (в них пароль SSH) — во временный файл; ядро удаляет его, прочитав.
        val paramsFile = File(ctx.cacheDir, "chain.json")
        paramsFile.writeText(paramsJSON)
        paramsFile.setReadable(false, false)
        paramsFile.setReadable(true, true)

        val log = File(ctx.cacheDir, "core.log")
        log.writeText("")
        val pb = ProcessBuilder(bin.absolutePath, "-socket", socketPath(ctx), "-params", paramsFile.absolutePath)
            .redirectErrorStream(true)
            .redirectOutput(ProcessBuilder.Redirect.appendTo(log))
        pb.environment()["TMPDIR"] = ctx.cacheDir.absolutePath
        val process = pb.start()

        val deadline = System.currentTimeMillis() + CONNECT_TIMEOUT_MS
        var last: Exception? = null
        while (System.currentTimeMillis() < deadline) {
            if (!process.isAlive) {
                throw IOException("ядро завершилось при запуске: ${readTail(ctx)}")
            }
            try {
                return Link.connect(socketPath(ctx), process)
            } catch (e: Exception) {
                last = e
                Thread.sleep(150)
            }
        }
        process.destroy()
        throw IOException("ядро не ответило за ${CONNECT_TIMEOUT_MS / 1000} с (${last?.message}). ${readTail(ctx)}")
    }

    /** Последние строки журнала ядра. */
    fun readTail(ctx: Context): String = runCatching {
        File(ctx.cacheDir, "core.log").readLines().filter { it.isNotBlank() }.takeLast(2).joinToString(" ")
    }.getOrDefault("")

    /** Журнал ядра: экран «Настройки» может его показать. */
    fun logFile(ctx: Context): File = File(ctx.cacheDir, "core.log")

    /**
     * Соединение с ядром. Читает один поток (служба), пишет другой — методы под замком.
     * read() возвращает null по таймауту.
     */
    class Link private constructor(
        private val socket: LocalSocket,
        private val process: Process,
        private val input: InputStream,
        private val output: OutputStream,
    ) : AutoCloseable {
        private val writeLock = Any()

        @Volatile private var closed = false

        /** Первое сообщение ядра: адрес, DNS, MTU, версия. */
        fun hello(): JSONObject {
            val f = read(HELLO_TIMEOUT_MS) ?: throw IOException("ядро не прислало параметры туннеля")
            if (f.type != T_HELLO) throw IOException("ядро ответило не тем, что ожидалось (${f.type})")
            return JSONObject(String(f.data, Charsets.UTF_8))
        }

        /** Отправить IP-пакет в туннель. */
        fun sendPacket(packet: ByteArray) {
            synchronized(writeLock) {
                if (closed) throw IOException("связь с ядром закрыта")
                writeFrame(output, T_PACKET, packet)
            }
        }

        /** Попросить ядро остановиться (перед закрытием). */
        fun sendStop() {
            runCatching { synchronized(writeLock) { writeFrame(output, T_STOP, ByteArray(0)) } }
        }

        /** Следующий кадр или null, если за timeoutMs ничего не пришло. */
        fun read(timeoutMs: Int): Frame? {
            if (closed) return null
            socket.soTimeout = timeoutMs
            val head = ByteArray(5)
            try {
                var got = 0
                while (got < 5) {
                    val n = input.read(head, got, 5 - got)
                    if (n < 0) return null
                    got += n
                }
            } catch (e: SocketTimeoutException) {
                return null
            } catch (e: IOException) {
                if (!closed) Log.w(TAG, "чтение из ядра: ${e.message}")
                return null
            }
            val len = ((head[1].toInt() and 0xff) shl 24) or ((head[2].toInt() and 0xff) shl 16) or
                ((head[3].toInt() and 0xff) shl 8) or (head[4].toInt() and 0xff)
            if (len < 0 || len > MAX_FRAME) {
                Log.e(TAG, "ядро прислало кадр неверной длины: $len")
                return null
            }
            val data = ByteArray(len)
            var have = 0
            socket.soTimeout = BODY_TIMEOUT_MS
            try {
                while (have < len) {
                    val n = input.read(data, have, len - have)
                    if (n < 0) return null
                    have += n
                }
            } catch (e: IOException) {
                Log.w(TAG, "кадр от ядра не дочитан: ${e.message}")
                return null
            }
            return Frame(head[0], data)
        }

        override fun close() {
            if (closed) return
            closed = true
            sendStopOnClose()
            runCatching { input.close() }
            runCatching { output.close() }
            runCatching { socket.close() }
            runCatching {
                if (!process.waitFor(2, TimeUnit.SECONDS)) process.destroy()
            }
        }

        private fun sendStopOnClose() {
            runCatching {
                socket.soTimeout = 1000
                synchronized(writeLock) { writeFrame(output, T_STOP, ByteArray(0)) }
            }
        }

        companion object {
            fun connect(path: String, process: Process): Link {
                val s = LocalSocket()
                s.connect(LocalSocketAddress(path, LocalSocketAddress.Namespace.FILESYSTEM))
                s.soTimeout = HELLO_TIMEOUT_MS
                return Link(s, process, s.inputStream, s.outputStream)
            }
        }
    }

    class Frame(val type: Byte, val data: ByteArray)

    // Типы кадров — те же, что в mobile/core/ipc.go
    const val T_HELLO = 0x10.toByte()
    const val T_LOG = 0x11.toByte()
    const val T_ERROR = 0x12.toByte()
    const val T_PACKET = 0x20.toByte()
    const val T_STATUS = 0x22.toByte()
    const val T_STATS = 0x24.toByte()
    const val T_STOP = 0x30.toByte()

    private const val HELLO_TIMEOUT_MS = 30_000
    private const val BODY_TIMEOUT_MS = 5_000
    private const val CONNECT_TIMEOUT_MS = 25_000L
    private const val MAX_FRAME = 1 shl 17

    private fun writeFrame(out: OutputStream, type: Byte, payload: ByteArray) {
        val buf = ByteArray(5 + payload.size)
        buf[0] = type
        buf[1] = ((payload.size ushr 24) and 0xff).toByte()
        buf[2] = ((payload.size ushr 16) and 0xff).toByte()
        buf[3] = ((payload.size ushr 8) and 0xff).toByte()
        buf[4] = (payload.size and 0xff).toByte()
        payload.copyInto(buf, 5)
        out.write(buf)
        out.flush()
    }
}
