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
import android.util.Log
import android.widget.Toast


import ru.usatyi.vpn.binding.ActivityMainBinding
import ru.usatyi.vpn.binding.ScreenConnectBinding
import ru.usatyi.vpn.binding.ScreenCreateBinding
import ru.usatyi.vpn.binding.ScreenSettingsBinding

/**
 * Главный экран: три вкладки («Подключиться», «Создать», «Настройки») и запуск/остановка туннеля.
 * Экраны сами рисуют себя по VpnState и KeyStore; здесь только действия, которые трогают систему.
 */
class MainActivity : android.app.Activity() {

    private lateinit var main: ActivityMainBinding
    private lateinit var connect: ConnectScreen
    private lateinit var server: ServerScreen
    private lateinit var settings: SettingsScreen

    /** Подключение, которое ждёт разрешения Android на VPN. */
    private var pendingStart: (() -> Unit)? = null

    /** Выбор файла .conf Amnezia в системном окне документов. */
    private val pickConf = object : FilePicker() {
        override fun onPicked(uri: android.net.Uri?) {
        if (uri == null) {
            VpnState.idle("Файл .conf не выбран")
            return
        }
        ChainUi.loadConf(activity!!, uri) { result ->
            runOnUiThread {
                toast(result)
                connect.onState()
            }
        }
        }
    }

    /** Открывает системный выбор файла для .conf Amnezia. */
    fun pickAmneziaFile() {
        if (isActive()) {
            toast("Сначала отключите VPN")
            return
        }
        runCatching {
            pickConf.launch()
        }.onFailure {
            toast("Не удалось открыть выбор файла: ${it.message}")
        }
    }
    private val handler = Handler(Looper.getMainLooper())

    private val vpnPermission = PermissionLauncher { _, resultCode ->
        val start = pendingStart
        pendingStart = null
        if (resultCode == RESULT_OK && start != null) {
            start()
        } else {
            VpnState.idle("Нет разрешения на VPN")
        }
    }

    private val notifPermission = object : SimpleLauncher() {
        override fun onResult() {}
    }

    private val stateListener: (VpnState.Snapshot) -> Unit = { runOnUiThread { connect.onState() } }

    /** Цепочка (Amnezia) меняет состояние чаще: обновляем экран так же. */
    private val chainListener: (Unit) -> Unit = { runOnUiThread { connect.onState() } }

    /** Раз в секунду обновляем таймер сеанса и проверяем связь с системой. */
    private val ticker = object : Runnable {
        override fun run() {
            connect.onTick()
            handler.postDelayed(this, 1000L)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Если что-то пойдёт не так при сборке экрана, приложение всё равно откроется
        // и покажет причину: без этого пользователь видит только «приложение закрыто».
        try {
            buildUi()
        } catch (e: Throwable) {
            Log.e("UsatyiVPN", "не удалось построить экран", e)
            showCrashScreen(e)
            return
        }
    }

    private fun buildUi() {
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

        main.bottomNav.setTitles(listOf("Подключиться", "Создать", "Настройки"))
        main.bottomNav.onPick = { index ->
            showScreen(index)
            true
        }
        showScreen(0)

        // Системные окна (выбор файла, разрешения) — через старый onActivityResult:
        // так приложение не тянет библиотеки AndroidX.
        pickConf.attach(this)
        vpnPermission.attach(this)
        notifPermission.attach(this)
        if (Build.VERSION.SDK_INT >= 33) {
            notifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    override fun onStart() {
        super.onStart()
        VpnState.addListener(stateListener)
        ChainState.addListener(chainListener)
    }

    override fun onStop() {
        VpnState.removeListener(stateListener)
        ChainState.removeListener(chainListener)
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

    /**
     * Аварийный экран: вместо закрытия приложения показываем, что именно сломалось.
     * Кнопка «Скопировать» отдаёт текст ошибки — по нему видно причину без отладки по кабелю.
     */
    private fun showCrashScreen(e: Throwable) {
        val error = Log.getStackTraceString(e)
        val pad = (16 * resources.displayMetrics.density).toInt()
        val text = android.widget.TextView(this).apply {
            this.text = "Приложение не смогло открыться.\n\n" +
                (e.message ?: e.javaClass.name) + "\n\n" +
                "Нажмите «Скопировать» и пришлите текст разработчику."
            textSize = 15f
            setTextIsSelectable(true)
            setPadding(pad, pad, pad, pad)
        }
        val copy = android.widget.Button(this).apply {
            setText("Скопировать ошибку")
            isAllCaps = false
            setOnClickListener {
                getSystemService(android.content.ClipboardManager::class.java)
                    ?.setPrimaryClip(ClipData.newPlainText("Ошибка UsatyiVPN", error))
                toast("Текст ошибки скопирован")
            }
        }
        val details = android.widget.TextView(this).apply {
            this.text = e.javaClass.name
            textSize = 13f
            setPadding(pad, 0, pad, pad)
        }
        val column = android.widget.LinearLayout(this).apply {
            orientation = android.widget.LinearLayout.VERTICAL
            addView(text)
            addView(copy)
            addView(details)
        }
        val scroll = android.widget.ScrollView(this).apply { addView(column) }
        setContentView(scroll)
    }

    /** 0 — «Подключиться», 1 — «Создать», 2 — «Настройки». */
    private fun showScreen(index: Int) {
        connect.setVisible(index == 0)
        server.setVisible(index == 1)
        if (index == 1) server.onShown()
        settings.setVisible(index == 2)
        if (index == 2) settings.onShown()
    }

    /** Результаты системных окон: выбор .conf и разрешение на VPN. */
    @Deprecated("Вызов системного механизма Android")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        pickConf.deliver(requestCode, resultCode, data)
        vpnPermission.deliver(requestCode, resultCode)
        notifPermission.deliver(requestCode)
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
        // Загружен .conf Amnezia — значит подключаемся цепочкой AmneziaWG → SSH → MTP,
        // а не напрямую к серверу: так пользователь и просил.
        val conf = AmneziaStore.load(this)
        if (conf != null && ok.transport != Transport.WG && ok.transport != Transport.SS) {
            startChain(key, ok, conf)
            return
        }
        if (conf != null && ok.transport == Transport.WG) {
            toast("Выбран способ WireGuard напрямую. Чтобы идти через Amnezia, выберите MTP/1–3")
        }
        val session = VpnState.Session(key.id, key.name, sk.host, ok.transport, ok.routing, ok.mode)
        if (ok.transport == Transport.WG) {
            VpnState.failed(WgController.notAvailable)
        } else {
            val intent = Intent(this, TunnelService::class.java)
                .setAction(TunnelService.ACTION_START)
                .putExtra(TunnelService.EXTRA_KEY_ID, key.id)
                .putExtra(TunnelService.EXTRA_NAME, key.name)
                .putExtra(TunnelService.EXTRA_KEY, key.text)
                .putExtra(TunnelService.EXTRA_TRANSPORT, ok.transport.name)
                .putExtra(TunnelService.EXTRA_ROUTING, ok.routing.name)
                .putStringArrayListExtra(TunnelService.EXTRA_APPS, ArrayList(key.apps))
            startForegroundService(this, intent)
        }
    }

    /** Подключение цепочкой: AmneziaWG по .conf, затем SSH и MTP. */
    private fun startChain(key: SavedKey, ok: Choice.Ok, conf: AwgConfig) {
        val session = VpnState.Session(key.id, key.name, conf.peer.host, ok.transport, ok.routing, ok.mode)
        VpnState.connecting(session, "Шаг 1 из 3: поднимаю AmneziaWG…")
        ChainState.stage("Шаг 1 из 3: AmneziaWG по файлу «${conf.name}»…")
        val intent = Intent(this, ChainService::class.java)
            .setAction(ChainService.ACTION_START)
            .putExtra(ChainService.EXTRA_KEY_ID, key.id)
            .putExtra(ChainService.EXTRA_NAME, key.name)
            .putExtra(ChainService.EXTRA_KEY, key.text)
            .putExtra(ChainService.EXTRA_TRANSPORT, ok.transport.name)
            .putExtra(ChainService.EXTRA_ROUTING, ok.routing.name)
            .putStringArrayListExtra(ChainService.EXTRA_APPS, ArrayList(key.apps))
        startForegroundService(this, intent)
    }

    /** Отключает текущий туннель (MTP — через службу, WireGuard — через библиотеку). */
    fun disconnect() {
        // Если цепочка поднимается или работает — гасим и её.
        if (ChainState.phase != ChainState.Phase.OFF) {
            startService(Intent(this, ChainService::class.java).setAction(ChainService.ACTION_STOP))
        }
        startService(Intent(this, TunnelService::class.java).setAction(TunnelService.ACTION_STOP))
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
