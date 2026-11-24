package ru.usatyi.vpn

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import ru.usatyi.vpn.databinding.ActivityMainBinding
import ru.usatyi.vpn.databinding.ScreenConnectBinding
import ru.usatyi.vpn.databinding.ScreenCreateBinding
import ru.usatyi.vpn.databinding.ScreenSettingsBinding

/**
 * Главный экран: три вкладки («Подключиться», «Создать», «Настройки») и запуск/остановка туннеля.
 * Экраны сами рисуют себя по VpnState и KeyStore; здесь только действия, которые трогают систему.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var main: ActivityMainBinding
    private lateinit var connect: ConnectScreen
    private lateinit var server: ServerScreen
    private lateinit var settings: SettingsScreen

    /** Подключение, которое ждёт разрешения Android на VPN. */
    private var pendingStart: (() -> Unit)? = null
    private val handler = Handler(Looper.getMainLooper())

    private val vpnPermission = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { res ->
        val start = pendingStart
        pendingStart = null
        if (res.resultCode == RESULT_OK && start != null) {
            start()
        } else {
            VpnState.idle("Нет разрешения на VPN")
        }
    }

    private val notifPermission = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { }

    private val stateListener: (VpnState.Snapshot) -> Unit = { runOnUiThread { connect.onState() } }

    /** Раз в секунду обновляем таймер сеанса и проверяем связь с системой. */
    private val ticker = object : Runnable {
        override fun run() {
            connect.onTick()
            handler.postDelayed(this, 1000L)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        main = ActivityMainBinding.inflate(layoutInflater)
        setContentView(main.root)

        val connectBinding = ScreenConnectBinding.inflate(layoutInflater, main.container, false)
        val createBinding = ScreenCreateBinding.inflate(layoutInflater, main.container, false)
        main.container.addView(connectBinding.root)
        main.container.addView(createBinding.root)
        val settingsBinding = ScreenSettingsBinding.inflate(layoutInflater, main.container, false)
        main.container.addView(settingsBinding.root)
        connect = ConnectScreen(this, connectBinding)
        server = ServerScreen(this, createBinding)
        settings = SettingsScreen(this, settingsBinding)

        main.bottomNav.setOnItemSelectedListener { item ->
            showScreen(item.itemId)
            true
        }
        showScreen(R.id.nav_connect)

        if (Build.VERSION.SDK_INT >= 33) {
            notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    override fun onStart() {
        super.onStart()
        VpnState.addListener(stateListener)
    }

    override fun onStop() {
        VpnState.removeListener(stateListener)
        super.onStop()
    }

    override fun onResume() {
        super.onResume()
        connect.onResume()
        handler.removeCallbacks(ticker)
        handler.post(ticker)
    }

    override fun onPause() {
        handler.removeCallbacks(ticker)
        super.onPause()
    }

    private fun showScreen(id: Int) {
        connect.setVisible(id == R.id.nav_connect)
        server.setVisible(id == R.id.nav_create)
        if (id == R.id.nav_create) server.onShown()
        settings.setVisible(id == R.id.nav_settings)
        if (id == R.id.nav_settings) settings.onShown()
    }

    // ---------- действия, которые трогают систему

    fun isActive(): Boolean {
        val p = VpnState.current.phase
        return p == VpnState.Phase.CONNECTED || p == VpnState.Phase.CONNECTING
    }

    /** Подключает ключ с его настройками. Сначала проверяет, что способ и приложения настроены. */
    fun connectKey(key: SavedKey) {
        if (isActive()) {
            toast("Сначала отключите текущее подключение")
            return
        }
        val sk = try {
            key.server
        } catch (e: Exception) {
            toast("Ключ повреждён: удалите его и добавьте заново")
            return
        }
        KeyStore.setSelected(this, key.id)
        // Режим (если расширенные настройки включены) решает способ и маршрутизацию для этого подключения
        val ok = when (val choice = Modes.choose(Modes.load(this), key.transport, key.routing) { sk.supports(it) }) {
            is Choice.Error -> {
                toast(choice.message)
                return
            }
            is Choice.Ok -> choice
        }
        if (!sk.supports(ok.transport)) {
            toast("В ключе нет параметров для «${ok.transport.label}». Выберите другой способ в настройках ключа")
            connect.showKeySettings(key)
            return
        }
        if (ok.transport == Transport.SS) {
            val link = ssLink(sk)
            if (link != null) copyText("ss-ссылка", link)
            toast("Ссылка ss:// скопирована. Вставьте её в приложение Shadowsocks")
            return
        }
        if (ok.routing == Routing.APPS && key.apps.isEmpty()) {
            toast("Выберите приложения для режима «Только выбранные приложения»")
            connect.showKeySettings(key)
            return
        }
        pendingStart = { startKey(key, sk, ok) }
        val permission = VpnService.prepare(this)
        if (permission != null) {
            vpnPermission.launch(permission)
        } else {
            val start = pendingStart
            pendingStart = null
            start?.invoke()
        }
    }

    private fun startKey(key: SavedKey, sk: ServerKey, ok: Choice.Ok) {
        val session = VpnState.Session(key.id, key.name, sk.host, ok.transport, ok.routing, ok.mode)
        if (ok.transport == Transport.WG) {
            VpnState.connecting(session, "Поднимаю WireGuard…")
            Thread {
                try {
                    val nonRu = if (ok.routing == Routing.NOT_RU) Routes.notRu(this) else emptyList()
                    WgController.up(this, WgConfig.build(sk, ok.routing, key.apps, nonRu))
                    VpnState.connected(session)
                } catch (e: Exception) {
                    VpnState.failed("WireGuard: ${e.message ?: e.javaClass.simpleName}")
                }
            }.start()
        } else {
            val intent = Intent(this, TunnelService::class.java)
                .setAction(TunnelService.ACTION_START)
                .putExtra(TunnelService.EXTRA_KEY_ID, key.id)
                .putExtra(TunnelService.EXTRA_NAME, key.name)
                .putExtra(TunnelService.EXTRA_KEY, key.text)
                .putExtra(TunnelService.EXTRA_TRANSPORT, ok.transport.name)
                .putExtra(TunnelService.EXTRA_ROUTING, ok.routing.name)
                .putStringArrayListExtra(TunnelService.EXTRA_APPS, ArrayList(key.apps))
            ContextCompat.startForegroundService(this, intent)
        }
    }

    /** Отключает текущий туннель (MTP — через службу, WireGuard — через библиотеку). */
    fun disconnect() {
        val session = VpnState.current.session
        if (session != null && session.transport == Transport.WG) {
            Thread {
                try {
                    WgController.down()
                } catch (e: Exception) {
                    // туннель уже опущен
                }
                VpnState.idle("Отключено")
            }.start()
        } else {
            startService(Intent(this, TunnelService::class.java).setAction(TunnelService.ACTION_STOP))
        }
    }

    /** Есть ли в системе активный VPN (нужно, чтобы не показывать «подключено» после отключения в настройках Android). */
    fun vpnTransportPresent(): Boolean {
        val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        return cm.allNetworks.any { cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true }
    }

    fun clipboardText(): String? {
        val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val text = cm.primaryClip
            ?.takeIf { it.itemCount > 0 }
            ?.getItemAt(0)
            ?.coerceToText(this)
            ?.toString()
            ?.trim()
        return if (text.isNullOrEmpty()) null else text
    }

    fun copyText(label: String, text: String) {
        val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        cm.setPrimaryClip(ClipData.newPlainText(label, text))
    }

    fun toast(msg: String) {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
    }
}
