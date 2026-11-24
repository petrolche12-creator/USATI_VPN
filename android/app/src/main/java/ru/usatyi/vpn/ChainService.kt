package ru.usatyi.vpn

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import androidx.core.app.NotificationCompat
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.IOException
import org.json.JSONObject

/**
 * Служба цепочки: [.conf Amnezia] → AmneziaWG → SSH на свой сервер → MTP-VPN.
 *
 * Сама она сетевой логики не содержит:
 *   • ядро (отдельный процесс, mobile/daemon) поднимает AmneziaWG из .conf, SSH и MTP;
 *   • служба только перекладывает IPv4-пакеты между системным VPN и ядром
 *     и публикует состояние для экрана.
 *
 * Так системный VPN остаётся тонким, а пакеты AmneziaWG идут мимо него
 * (приложение исключено из своего же VPN — addDisallowedApplication), без петли.
 */
class ChainService : VpnService() {

    private class Plan(
        val keyId: String,
        val name: String,
        val keyText: String,
        val transport: Transport,
        val routing: Routing,
        val apps: Set<String>,
    )

    private val stop = StopReason()
    @Volatile private var tun: ParcelFileDescriptor? = null
    @Volatile private var link: CoreProcess.Link? = null
    private var worker: Thread? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> {
                if (worker?.isAlive == true) return START_NOT_STICKY
                val plan = Plan(
                    keyId = intent.getStringExtra(EXTRA_KEY_ID).orEmpty(),
                    name = intent.getStringExtra(EXTRA_NAME).orEmpty(),
                    keyText = intent.getStringExtra(EXTRA_KEY).orEmpty(),
                    transport = Transport.valueOf(intent.getStringExtra(EXTRA_TRANSPORT) ?: Transport.MTP2.name),
                    routing = Routing.valueOf(intent.getStringExtra(EXTRA_ROUTING) ?: Routing.ALL.name),
                    apps = intent.getStringArrayListExtra(EXTRA_APPS)?.toSet().orEmpty(),
                )
                showForeground("Подключаюсь…")
                stop.reset()
                ChainState.stage("Запускаю ядро…")
                worker = Thread({ runChain(plan) }, "usatyi-chain").also { it.start() }
            }
            ACTION_STOP -> {
                stopTunnel()
                if (worker?.isAlive != true) {
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf()
                }
            }
        }
        return START_NOT_STICKY
    }

    override fun onRevoke() {
        stopTunnel()
    }

    override fun onDestroy() {
        stopTunnel()
        super.onDestroy()
    }

    private fun stopTunnel() {
        stop.stop()
        closeAll()
    }

    private fun closeAll() {
        runCatching {
            tun?.close()
        }
        tun = null
        runCatching {
            link?.sendStop()
            link?.close()
        }
        link = null
    }

    private fun runChain(plan: Plan) {
        try {
            val key = KeyCodec.decode(plan.keyText)
            val owner = Settings.owner(this) ?: throw MtpException(
                "Не заполнены данные SSH к серверу. Откройте «Создать» → «Настроить сервер».",
            )
            if (plan.routing == Routing.APPS && plan.apps.isEmpty()) {
                throw MtpException("не выбрано ни одного приложения для VPN")
            }
            if (!CoreProcess.available(this)) throw MtpException(CoreProcess.missingReason(this))
            val conf = AmneziaStore.load(this) ?: throw MtpException(
                "Не загружен файл Amnezia (.conf). Нажмите «Добавить Amnezia» на вкладке «Подключиться».",
            )

            val session = VpnState.Session(plan.keyId, plan.name, key.host, plan.transport, plan.routing)
            VpnState.connecting(session, "Поднимаю цепочку…")

            val params = JSONObject()
                .put("conf", AmneziaStore.rawText(this))
                .put("key", plan.keyText)
                .put("transport", when (plan.transport) {
                    Transport.MTP1 -> "MTP2" // UDP не проходит через SSH-канал, берём тот же протокол поверх TCP
                    Transport.MTP3 -> "MTP3"
                    else -> "MTP2"
                })
                .put("asVpn", AmneziaStore.asVpn(this))
                .put(
                    "ssh",
                    JSONObject()
                        .put("host", owner.host)
                        .put("port", owner.sshPort)
                        .put("user", owner.sshUser)
                        .put("pass", owner.sshPass)
                        .put("fp", owner.hostFp),
                )

            ChainState.stage("Шаг 1 из 3: AmneziaWG по файлу ${conf.name}…")
            showForeground("AmneziaWG → SSH → MTP…")
            val l = CoreProcess.spawn(this, params.toString())
            link = l
            val hello = l.hello()
            val addr = hello.optString("addr")
            val dns = hello.optString("dns")
            val mtu = hello.optInt("mtu", 1380).coerceIn(576, 1500)
            if (addr.isEmpty()) throw MtpException("ядро не сообщило адрес в туннеле")

            ChainState.waitingVpn("Ядро готово: ${hello.optString("version")}. Поднимаю системный VPN…")
            val builder = Builder()
                .setSession("Усатый VPN")
                .setMtu(mtu)
                .addAddress(addr, 32)
                .addAddress("fd10:8::2", 128)
            if (dns.isNotEmpty()) builder.addDnsServer(dns)
            // Своё приложение — мимо VPN: иначе пакеты AmneziaWG уйдут в наш же туннель.
            builder.addDisallowedApplication(packageName)
            val nonRu = if (plan.routing == Routing.NOT_RU) Routes.notRu(this) else emptyList()
            Routes.apply(builder, plan.routing, plan.apps - packageName, packageName, nonRu, excludeSelf = false)
            val pfd = builder.establish() ?: throw MtpException("система не разрешила VPN")
            tun = pfd

            showForeground("Через Amnezia: ${conf.name}")
            VpnState.connected(session)
            ChainState.running()
            pump(pfd, l)
        } catch (e: MtpRejected) {
            stop.fail("Сервер не пустил: ${e.message}")
        } catch (e: MtpKicked) {
            stop.fail("Сервер отключил ключ: ${e.message}")
        } catch (e: MtpException) {
            // Свои ошибки (нет .conf, нет ядра, ядро молчит) — без лишней приставки.
            stop.fail(e.message ?: "ошибка подключения")
        } catch (e: Exception) {
            stop.fail(e.message ?: e.javaClass.simpleName)
        } finally {
            stop.stop()
            closeAll()
            val f = stop.failure
            if (f == null) VpnState.idle("Отключено") else {
                VpnState.failed(f)
                ChainState.fail(f)
            }
            if (f == null) ChainState.off()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    /** Пакеты из интерфейса — в ядро; пакеты из ядра — в интерфейс. */
    private fun pump(pfd: ParcelFileDescriptor, l: CoreProcess.Link) {
        val tunIn = FileInputStream(pfd.fileDescriptor)
        val tunOut = FileOutputStream(pfd.fileDescriptor)
        Thread({ readTun(tunIn, l) }, "usatyi-tun-read").apply {
            isDaemon = true
            start()
        }

        var lastPublish = 0L
        var silent = 0
        while (!stop.stopping) {
            val frame = l.read(500)
            if (frame == null) {
                // Нет данных. Раз в секунду обновляем счётчики; если ядро молчит слишком
                // долго (совсем не отвечает), считаем связь потерянной.
                val now = System.currentTimeMillis()
                if (now - lastPublish >= 1000L) {
                    lastPublish = now
                    ChainState.traffic(ChainState.awgRx, ChainState.awgTx, ChainState.rx, ChainState.tx)
                }
                silent += 500
                if (silent >= CORE_SILENT_MS) throw MtpException("ядро перестало отвечать")
                continue
            }
            silent = 0
            when (frame.type) {
                CoreProcess.T_PACKET -> {
                    runCatching { tunOut.write(frame.data) }
                }
                CoreProcess.T_STATUS -> {
                    val j = JSONObject(String(frame.data, Charsets.UTF_8))
                    VpnState.server(j.optLong("used"), j.optLong("limit"), j.optLong("expires"))
                }
                CoreProcess.T_STATS -> {
                    val j = JSONObject(String(frame.data, Charsets.UTF_8))
                    ChainState.traffic(
                        j.optLong("awgRx"), j.optLong("awgTx"), j.optLong("rx"), j.optLong("tx"),
                    )
                    VpnState.traffic(j.optLong("rx"), j.optLong("tx"))
                }
                CoreProcess.T_LOG -> {
                    CoreState.log(String(frame.data, Charsets.UTF_8))
                }
                CoreProcess.T_ERROR -> {
                    val msg = String(frame.data, Charsets.UTF_8)
                    stop.fail(msg)
                    return
                }
                else -> Unit
            }
        }
    }

    private fun readTun(tunIn: FileInputStream, l: CoreProcess.Link) {
        val buf = ByteArray(2048)
        try {
            while (!stop.stopping) {
                val n = tunIn.read(buf)
                if (n < 0) {
                    stop.fail("интерфейс VPN закрыт системой")
                    break
                }
                if (n == 0) continue
                if (((buf[0].toInt() and 0xff) shr 4) != 4) continue // IPv6 не туннелируем
                l.sendPacket(buf.copyOf(n))
            }
        } catch (e: IOException) {
            // При остановке интерфейс закрывают, и чтение падает: это не ошибка туннеля.
            stop.fail("ошибка туннеля: ${e.message}")
        }
    }

    private fun showForeground(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        if (nm.getNotificationChannel(CHANNEL_ID) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "VPN", NotificationManager.IMPORTANCE_LOW),
            )
        }
        val openApp = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE,
        )
        val stopIntent = PendingIntent.getService(
            this, 1,
            Intent(this, ChainService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_power)
            .setContentTitle("Усатый VPN · Amnezia → SSH → MTP")
            .setContentText(text)
            .setContentIntent(openApp)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .addAction(0, "Отключить", stopIntent)
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(NOTIF_ID, notification)
        }
    }

    companion object {
        const val ACTION_START = "ru.usatyi.vpn.CHAIN_START"
        const val ACTION_STOP = "ru.usatyi.vpn.CHAIN_STOP"
        const val EXTRA_KEY_ID = "key_id"
        const val EXTRA_NAME = "name"
        const val EXTRA_KEY = "key"
        const val EXTRA_TRANSPORT = "transport"
        const val EXTRA_ROUTING = "routing"
        const val EXTRA_APPS = "apps"

        private const val CHANNEL_ID = "chain"
        private const val NOTIF_ID = 2
        private const val CORE_SILENT_MS = 30_000
    }
}

/** Журнал ядра для экрана (последние строки, чтобы показать причину сбоя). */
object CoreState {
    private val lines = ArrayDeque<String>()

    @Synchronized
    fun log(line: String) {
        if (line.isBlank()) return
        lines.addLast(line)
        while (lines.size > 60) lines.removeFirst()
    }

    @Synchronized
    fun tail(n: Int): List<String> = lines.takeLast(n)
}
