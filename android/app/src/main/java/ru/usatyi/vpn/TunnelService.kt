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
import java.util.concurrent.atomic.AtomicLong
import org.json.JSONObject

/**
 * Системный VPN для протоколов MTP/1, MTP/2 и MTP/3: читает IPv4-пакеты из виртуального
 * интерфейса и отправляет их на сервер через выбранный канал (UDP или TCP).
 * Состояние и счётчики трафика публикуются в VpnState.
 */
class TunnelService : VpnService() {

    private class Plan(
        val keyId: String,
        val name: String,
        val keyText: String,
        val transport: Transport,
        val routing: Routing,
        val apps: Set<String>,
    )

    private val stop = StopReason()
    @Volatile private var lastTx = 0L
    @Volatile private var tun: ParcelFileDescriptor? = null
    @Volatile private var link: Link? = null
    private val rxBytes = AtomicLong(0)
    private val txBytes = AtomicLong(0)
    private var worker: Thread? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> {
                if (worker?.isAlive == true) return START_NOT_STICKY
                val plan = Plan(
                    keyId = intent.getStringExtra(EXTRA_KEY_ID).orEmpty(),
                    name = intent.getStringExtra(EXTRA_NAME).orEmpty(),
                    keyText = intent.getStringExtra(EXTRA_KEY).orEmpty(),
                    transport = Transport.valueOf(intent.getStringExtra(EXTRA_TRANSPORT) ?: Transport.MTP1.name),
                    routing = Routing.valueOf(intent.getStringExtra(EXTRA_ROUTING) ?: Routing.ALL.name),
                    apps = intent.getStringArrayListExtra(EXTRA_APPS)?.toSet().orEmpty(),
                )
                showForeground("Подключаюсь…")
                stop.reset()
                rxBytes.set(0)
                txBytes.set(0)
                worker = Thread({ runTunnel(plan) }, "usatyi-tunnel").also { it.start() }
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
        try {
            tun?.close()
        } catch (e: IOException) {
            // уже закрыт
        }
        tun = null
        try {
            link?.close()
        } catch (e: Exception) {
            // уже закрыт
        }
        link = null
    }

    private fun sessionOf(plan: Plan, key: ServerKey) =
        VpnState.Session(plan.keyId, plan.name, key.host, plan.transport, plan.routing)

    private fun runTunnel(plan: Plan) {
        try {
            val key = KeyCodec.decode(plan.keyText)
            val port = key.portFor(plan.transport)
                ?: throw MtpException("в ключе нет порта для «${plan.transport.label}»")
            if (plan.routing == Routing.APPS && plan.apps.isEmpty()) {
                throw MtpException("не выбрано ни одного приложения для VPN")
            }
            VpnState.connecting(sessionOf(plan, key), "Подключаюсь к ${key.host}…")
            val l: Link = when (plan.transport) {
                Transport.MTP1 -> UdpLink.open(key.host, port) { protect(it) }
                Transport.MTP2 -> TcpLink.open(key.host, port, false) { protect(it) }
                Transport.MTP3 -> TcpLink.open(key.host, port, true) { protect(it) }
                else -> throw MtpException("этот способ не работает через туннель MTP")
            }
            link = l
            val client = MtpClient(key, l)
            val w = client.handshakeFor(plan.transport)

            val nonRu = if (plan.routing == Routing.NOT_RU) Routes.notRu(this) else emptyList()
            val builder = Builder()
                .setSession("Усатый VPN")
                .setMtu(w.mtu)
                .addAddress(ipText(w.ip), 24)
                .addDnsServer(ipText(w.dns))
                .addAddress("fd10:8::2", 128)
            Routes.apply(builder, plan.routing, plan.apps, packageName, nonRu)
            val pfd = builder.establish() ?: throw MtpException("система не разрешила VPN")
            tun = pfd

            showForeground("Подключено к ${key.host}")
            VpnState.connected(sessionOf(plan, key))
            pump(pfd, l, client)
        } catch (e: MtpKicked) {
            stop.fail("Сервер отключил ключ: ${e.message}")
        } catch (e: MtpRejected) {
            stop.fail("Сервер не пустил: ${e.message}")
        } catch (e: Exception) {
            stop.fail(e.message ?: e.javaClass.simpleName)
        } finally {
            // Сначала отмечаем остановку, потом закрываем: закрытие не должно попасть в причину ошибки.
            stop.stop()
            closeAll()
            val f = stop.failure
            if (f == null) VpnState.idle("Отключено") else VpnState.failed(f)
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    /** Основной цикл: приём кадров с сервера, keepalive, счётчики и контроль связи. */
    private fun pump(pfd: ParcelFileDescriptor, l: Link, client: MtpClient) {
        val tunIn = FileInputStream(pfd.fileDescriptor)
        val tunOut = FileOutputStream(pfd.fileDescriptor)
        Thread({ readTun(tunIn, tunOut, l, client) }, "usatyi-tun-read").apply {
            isDaemon = true
            start()
        }

        var lastRx = System.currentTimeMillis()
        lastTx = lastRx
        var lastPublish = 0L
        while (!stop.stopping) {
            val frame = try {
                l.recv(1000)
            } catch (e: IOException) {
                if (stop.stopping) break
                throw IOException("связь с сервером прервалась: ${e.message}", e)
            }
            val now = System.currentTimeMillis()
            if (now - lastPublish >= 1000L) {
                VpnState.traffic(rxBytes.get(), txBytes.get())
                lastPublish = now
            }
            if (frame == null) {
                if (now - lastRx > LINK_DEAD_MS) throw MtpException("сервер молчит больше 90 секунд")
                if (now - lastTx > KEEPALIVE_MS) {
                    sendQuietly(l, client.keepalive())
                    lastTx = now
                }
                continue
            }
            val r = client.decrypt(frame, frame.size) ?: continue
            lastRx = now
            when (r.first) {
                Mtp.K_IP -> {
                    tunOut.write(r.second)
                    rxBytes.addAndGet(r.second.size.toLong())
                }
                Mtp.K_STATUS -> onStatus(r.second)
                Mtp.K_KICK -> throw MtpKicked(if (r.second.isNotEmpty()) (r.second[0].toInt() and 0xff) else 0)
            }
        }
    }

    /** Читает пакеты из интерфейса и отправляет их на сервер. */
    private fun readTun(tunIn: FileInputStream, tunOut: FileOutputStream, l: Link, client: MtpClient) {
        val buf = ByteArray(2048)
        try {
            while (!stop.stopping) {
                val n = tunIn.read(buf)
                if (n < 0) {
                    stop.fail("интерфейс VPN закрыт системой")
                    break
                }
                if (n == 0) continue
                // IPv6 не туннелируем: сервер его не принимает. Но и молча отбрасывать нельзя —
                // приложения, которые сначала пробуют IPv6, ждали бы таймаут. Отвечаем ICMPv6
                // «адрес недоступен», и приложение сразу переходит на IPv4.
                if (((buf[0].toInt() and 0xff) shr 4) != 4) {
                    val reply = Icmp6.unreachable(buf, n)
                    if (reply != null) {
                        try {
                            tunOut.write(reply)
                        } catch (e: IOException) {
                            // интерфейс уже закрыт остановкой: ответ не нужен
                        }
                    }
                    continue
                }
                sendQuietly(l, client.encrypt(Mtp.K_IP, buf, 0, n))
                txBytes.addAndGet(n.toLong())
                lastTx = System.currentTimeMillis()
            }
        } catch (e: IOException) {
            // При остановке интерфейс закрывают, и чтение падает: это не ошибка туннеля.
            // fail() ничего не запишет, если остановка уже начата.
            stop.fail("ошибка туннеля: ${e.message}")
        }
    }

    private fun sendQuietly(l: Link, frame: ByteArray) {
        try {
            l.send(frame)
        } catch (e: IOException) {
            // временный сбой сети: пакет потерян, TCP его переотправит
        }
    }

    /** STATUS от сервера: сколько уже израсходовано и каков лимит (0 — без лимита, срок 0 — без срока). */
    private fun onStatus(payload: ByteArray) {
        try {
            val j = JSONObject(String(payload, Charsets.UTF_8))
            VpnState.server(j.optLong("used"), j.optLong("limit"), j.optLong("expires"))
        } catch (e: Exception) {
            // неизвестный формат статуса: данные не обновляем
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
            Intent(this, TunnelService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_power)
            .setContentTitle("Усатый VPN")
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
        const val ACTION_START = "ru.usatyi.vpn.START"
        const val ACTION_STOP = "ru.usatyi.vpn.STOP"
        const val EXTRA_KEY_ID = "key_id"
        const val EXTRA_NAME = "name"
        const val EXTRA_KEY = "key"
        const val EXTRA_TRANSPORT = "transport"
        const val EXTRA_ROUTING = "routing"
        const val EXTRA_APPS = "apps"

        private const val CHANNEL_ID = "tunnel"
        private const val NOTIF_ID = 1
        private const val KEEPALIVE_MS = 20_000L
        private const val LINK_DEAD_MS = 90_000L
    }
}

private fun ipText(b: ByteArray): String = b.joinToString(".") { (it.toInt() and 0xff).toString() }
