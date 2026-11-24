package ru.usatyi.vpn

import android.content.Intent
import android.view.View

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import org.json.JSONObject
import ru.usatyi.vpn.binding.DialogCreateKeyBinding
import ru.usatyi.vpn.binding.DialogKeyResultBinding
import ru.usatyi.vpn.binding.DialogServerSetupBinding
import ru.usatyi.vpn.binding.DialogTextBinding
import ru.usatyi.vpn.binding.ItemServerKeyBinding
import ru.usatyi.vpn.binding.ScreenCreateBinding

/**
 * Вкладка «Создать»: состояние сервера, установка по SSH и ключи, которые выданы друзьям.
 * Все действия над ключами идут через админ-утилиту на сервере.
 */
class ServerScreen(private val act: MainActivity, private val b: ScreenCreateBinding) {

    private var list: JSONObject? = null
    private var error: String? = null
    private var loading = false
    private var checkedAt = 0L

    init {
        b.serverSetupBtn.setOnClickListener { showSetupDialog() }
        b.serverCheckBtn.setOnClickListener { refresh() }
        b.serverForgetBtn.setOnClickListener {
            Ui.confirm(
                act,
                "Забыть сервер на телефоне? Сам сервер не меняется, ключи на нём продолжают работать.",
                "Забыть",
            ) {
                Settings.clearOwner(act)
                list = null
                error = null
                render()
            }
        }
        b.createKeyBtn.setOnClickListener { showCreateKeyDialog() }
        render()
    }

    fun setVisible(v: Boolean) {
        b.root.visibility = if (v) View.VISIBLE else View.GONE
    }

    /** Открыли вкладку: если сервер настроен и список ещё не получен, обновляем его. */
    fun onShown() {
        if (Settings.owner(act) != null && list == null && !loading) refresh()
    }

    /** Запрашивает список ключей у сервера. Можно вызывать из любого потока. */
    fun refresh() {
        val owner = Settings.owner(act)
        if (owner == null) {
            act.runOnUiThread {
                list = null
                error = null
                render()
            }
            return
        }
        if (loading) return
        loading = true
        act.runOnUiThread { render() }
        Thread {
            var result: JSONObject? = null
            var err: String? = null
            try {
                result = Admin.list(owner)
            } catch (e: Exception) {
                err = e.message ?: e.javaClass.simpleName
            }
            act.runOnUiThread {
                loading = false
                if (result != null) {
                    list = result
                    error = null
                    checkedAt = System.currentTimeMillis()
                } else {
                    error = err
                }
                render()
            }
        }.start()
    }

    // ---------- карточка сервера и список ключей

    private fun render() {
        val owner = Settings.owner(act)
        if (owner == null) {
            b.serverTitle.text = "Сервер не настроен"
            b.serverStatus.text = "Укажите SSH-доступ к VPS. Установщик поставит сервер сам: " +
                "MTP/1, MTP/2, MTP/3, WireGuard и Shadowsocks."
            b.serverSetupBtn.text = "Настроить сервер"
            b.serverCheckBtn.visibility = View.GONE
            b.serverForgetBtn.visibility = View.GONE
            b.createKeyBtn.isEnabled = false
        } else {
            b.serverTitle.text = "Сервер ${owner.host}"
            b.serverSetupBtn.text = "Переустановить или обновить"
            b.serverCheckBtn.visibility = View.VISIBLE
            b.serverForgetBtn.visibility = View.VISIBLE
            b.createKeyBtn.isEnabled = list != null
            b.serverStatus.text = when {
                loading -> "Проверяю связь с сервером…"
                error != null -> "Нет связи с сервером: $error\nПроверьте адрес и SSH-доступ, затем нажмите «Обновить»."
                list != null -> summaryText()
                else -> "Нажмите «Обновить», чтобы проверить сервер."
            }
        }
        renderKeys()
    }

    private fun summaryText(): String {
        val j = list ?: return ""
        val users = j.optJSONArray("users")
        val total = users?.length() ?: 0
        var online = 0
        if (users != null) {
            for (i in 0 until users.length()) {
                if (users.getJSONObject(i).optBoolean("online")) online++
            }
        }
        val time = SimpleDateFormat("HH:mm", Locale.getDefault()).format(Date(checkedAt))
        return "Работает · VPN UDP ${j.optInt("port")} · ключей: $total, сейчас в сети: $online · проверено в $time"
    }

    private fun renderKeys() {
        val users = list?.optJSONArray("users")
        val count = users?.length() ?: 0
        b.serverKeysEmpty.text = when {
            Settings.owner(act) == null -> "Сначала настройте сервер."
            list == null -> "Список ключей появится после проверки сервера."
            else -> "Ключей пока нет. Нажмите «Создать ключ»."
        }
        b.serverKeysEmpty.visibility = if (count == 0) View.VISIBLE else View.GONE
        b.serverKeyList.removeAllViews()
        if (users == null) return
        for (i in 0 until users.length()) {
            b.serverKeyList.addView(buildServerRow(users.getJSONObject(i)))
        }
    }

    private fun buildServerRow(u: JSONObject): View {
        val r = ItemServerKeyBinding.inflate(act.layoutInflater, b.serverKeyList, false)
        val name = u.optString("name")
        val status = u.optString("status")
        val limit = u.optLong("limit")
        val used = u.optLong("used")
        val expires = u.optLong("expires")
        r.skName.text = name
        r.skStatus.text = status
        r.skStatus.setTextColor(
            when (status) {
                "активен" -> Ui.accent()
                "отключён" -> Theme.onSurfaceVariant
                else -> Theme.errorContainer
            },
        )
        r.skTraffic.text = "${Ui.bytes(used)} из ${limitText(limit)} · срок: ${dateText(expires)}"
        r.skOnline.text = if (u.optBoolean("online")) "● сейчас в сети" else "не в сети"
        r.skActionsBtn.setOnClickListener { showActions(u) }
        return r.root
    }

    /** Действия над ключом на сервере. */
    private fun showActions(u: JSONObject) {
        val owner = Settings.owner(act) ?: return
        val name = u.optString("name")
        val disabled = u.optString("status") == "отключён"
        val items = arrayOf(
            "Показать ключ",
            if (disabled) "Включить" else "Выключить",
            "Сбросить счётчик трафика",
            "Удалить ключ",
        )
        DialogBuilder(act)
            .setTitle("Ключ «$name»")
            .setItems(items) { _, which ->
                when (which) {
                    0 -> showKey(owner, name)
                    1 -> runAdmin(if (disabled) "Ключ включён" else "Ключ выключен") {
                        Admin.setEnabled(owner, name, disabled)
                    }
                    2 -> Ui.confirm(act, "Обнулить счётчик трафика ключа «$name»?", "Сбросить") {
                        runAdmin("Счётчик сброшен") { Admin.resetTraffic(owner, name) }
                    }
                    3 -> Ui.confirm(act, "Удалить ключ «$name» на сервере? Друг потеряет доступ.", "Удалить") {
                        runAdmin("Ключ удалён") { Admin.delete(owner, name) }
                    }
                }
            }
            .setNegativeButton("Закрыть", null)
            .show()
    }

    /** Выполняет действие на сервере в фоне, потом обновляет список. */
    private fun runAdmin(success: String, action: () -> Unit) {
        Thread {
            try {
                action()
                act.runOnUiThread { act.toast(success) }
                refresh()
            } catch (e: Exception) {
                act.runOnUiThread { act.toast("Не получилось: ${e.message ?: e.javaClass.simpleName}") }
            }
        }.start()
    }

    private fun showKey(owner: Owner, name: String) {
        Thread {
            try {
                val key = Admin.show(owner, name).getString("key")
                act.runOnUiThread { showKeyResult(name, key, created = false) }
            } catch (e: Exception) {
                act.runOnUiThread { act.toast("Не получилось: ${e.message ?: e.javaClass.simpleName}") }
            }
        }.start()
    }

    // ---------- создание ключа

    private fun showCreateKeyDialog() {
        val owner = Settings.owner(act) ?: return
        val d = DialogCreateKeyBinding.inflate(act.layoutInflater)
        d.ckDays.setText("30")
        d.ckLimit.setText("10")
        DialogBuilder(act)
            .setTitle("Новый ключ")
            .setView(d.root)
            .setPositiveButton("Создать") { _, _ ->
                val name = d.ckName.text.toString().trim()
                val days = d.ckDays.text.toString().trim().toIntOrNull()
                val limit = d.ckLimit.text.toString().trim().replace(',', '.').toDoubleOrNull()
                when {
                    !NAME_RE.matches(name) -> act.toast("Имя: от 1 до 40 символов, буквы, цифры, пробел, точка, _ и -")
                    days == null || days !in 0..3650 -> act.toast("Срок: от 0 до 3650 дней, 0 — без срока")
                    limit == null || limit < 0.0 || limit > 100_000.0 -> act.toast("Лимит: число ГБ, 0 — без лимита")
                    else -> createKey(owner, name, days, limit)
                }
            }
            .setNegativeButton("Отмена", null)
            .show()
    }

    private fun createKey(owner: Owner, name: String, days: Int, limit: Double) {
        Thread {
            try {
                val key = Admin.add(owner, name, days, limit).getString("key")
                act.runOnUiThread { showKeyResult(name, key, created = true) }
                refresh()
            } catch (e: Exception) {
                act.runOnUiThread { act.toast("Ключ не создан: ${e.message ?: e.javaClass.simpleName}") }
            }
        }.start()
    }

    /** Окно с готовым ключом: скопировать, отправить, а для нового ключа — добавить себе. */
    private fun showKeyResult(name: String, key: String, created: Boolean) {
        val d = DialogKeyResultBinding.inflate(act.layoutInflater)
        d.dkText.text = key
        d.dkAddSelf.visibility = if (created) View.VISIBLE else View.GONE
        val dialog = DialogBuilder(act)
            .setTitle(if (created) "Ключ «$name» создан" else "Ключ «$name»")
            .setView(d.root)
            .create()
        d.dkCopy.setOnClickListener {
            act.copyText("mtp-key", key)
            act.toast("Ключ скопирован")
        }
        d.dkShare.setOnClickListener {
            val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, key)
            act.startActivity(Intent.createChooser(send, "Отправить ключ"))
        }
        d.dkAddSelf.setOnClickListener {
            try {
                val r = KeyStore.addOrUpdate(act, key)
                KeyStore.setSelected(act, r.key.id)
                act.toast("Ключ добавлен в этот телефон")
            } catch (e: Exception) {
                act.toast("Не получилось добавить: ${e.message}")
            }
            dialog.dismiss()
        }
        d.dkClose.setOnClickListener { dialog.dismiss() }
        dialog.show()
    }

    // ---------- установка и настройка сервера

    private fun showSetupDialog() {
        val old = Settings.owner(act)
        val d = DialogServerSetupBinding.inflate(act.layoutInflater)
        d.ssHost.setText(old?.host.orEmpty())
        d.ssUser.setText(old?.sshUser ?: "root")
        d.ssSshPort.setText((old?.sshPort ?: 22).toString())
        d.ssVpnPort.setText((old?.vpnPort ?: 443).toString())
        d.ssPass.setText(old?.sshPass.orEmpty())
        DialogBuilder(act)
            .setTitle(if (old == null) "Настроить сервер" else "Переустановить сервер")
            .setView(d.root)
            .setPositiveButton("Установить") { _, _ ->
                val form = readSetup(d)
                if (form != null) startInstall(form)
            }
            .setNegativeButton("Отмена", null)
            .show()
    }

    /** Проверяет поля формы. Возвращает null и подсказку, если что-то не так. */
    private fun readSetup(d: DialogServerSetupBinding): Owner? {
        val host = d.ssHost.text.toString().trim()
        if (!HOST_RE.matches(host)) {
            act.toast("Укажите IP или домен сервера")
            return null
        }
        val user = d.ssUser.text.toString().trim().ifEmpty { "root" }
        if (!USER_RE.matches(user)) {
            act.toast("Некорректное имя пользователя SSH")
            return null
        }
        val sshPort = d.ssSshPort.text.toString().trim().toIntOrNull()
        val vpnPort = d.ssVpnPort.text.toString().trim().toIntOrNull()
        if (sshPort == null || vpnPort == null || sshPort !in 1..65535 || vpnPort !in 1..65535) {
            act.toast("Порты — числа от 1 до 65535")
            return null
        }
        val pass = d.ssPass.text.toString()
        if (pass.isEmpty()) {
            act.toast("Введите пароль SSH")
            return null
        }
        val known = Settings.owner(act)?.takeIf { it.host == host && it.sshPort == sshPort }
        return Owner(host, sshPort, user, pass, vpnPort, user == "root", known?.hostFp.orEmpty())
    }

    /** Установка с живым журналом: окно не закрывается, пока установка не закончится. */
    private fun startInstall(form: Owner) {
        val t = DialogTextBinding.inflate(act.layoutInflater)
        val dialog = DialogBuilder(act)
            .setTitle("Установка на ${form.host}")
            .setView(t.root)
            .setCancelable(false)
            .setPositiveButton("Готово", null)
            .create()
        dialog.show()
        val ok = dialog.getButton(android.app.AlertDialog.BUTTON_POSITIVE)
        ok.isEnabled = false
        appendLog(t, "Установка: VPN UDP ${form.vpnPort}, вход ${form.sshUser}@${form.host}")
        Thread {
            try {
                val mtpPy = act.assets.open("server/mtp.py").use { it.readBytes() }
                val installSh = act.assets.open("server/install.sh").use { it.readBytes() }
                val owner = Admin.install(form, mtpPy, installSh) { line ->
                    act.runOnUiThread { appendLog(t, line) }
                }
                Settings.saveOwner(act, owner)
                addOwnerKey(owner) { line -> act.runOnUiThread { appendLog(t, line) } }
                act.runOnUiThread {
                    appendLog(t, "Готово: сервер установлен и запущен")
                    ok.isEnabled = true
                    list = null
                    refresh()
                }
            } catch (e: Exception) {
                act.runOnUiThread {
                    appendLog(t, "Ошибка: ${e.message ?: e.javaClass.simpleName}")
                    ok.isEnabled = true
                }
            }
        }.start()
    }

    /**
     * Создаёт ключ владельца на только что установленном сервере и сразу добавляет его на телефон.
     * Ошибка не отменяет установку: сервер уже работает, ключ можно создать вручную на вкладке «Создать».
     */
    private fun addOwnerKey(owner: Owner, line: (String) -> Unit) {
        var lastError = "неизвестная ошибка"
        for (i in 1..5) {
            val name = if (i == 1) OWNER_KEY else "$OWNER_KEY $i"
            line("создаю ключ «$name» на сервере…")
            try {
                val key = Admin.add(owner, name, 0, 0.0).getString("key")
                val added = KeyStore.addOrUpdate(act, key)
                KeyStore.setSelected(act, added.key.id)
                line("ключ «$name» добавлен на телефон и выбран")
                return
            } catch (e: Exception) {
                lastError = e.message ?: e.javaClass.simpleName
                if (!lastError.contains("уже")) break
            }
        }
        line("ключ владельца не создан: $lastError. Создайте его на вкладке «Создать».")
    }

    private fun appendLog(t: DialogTextBinding, line: String) {
        t.dtText.append(line + "\n")
        t.dtScroll.post { t.dtScroll.fullScroll(View.FOCUS_DOWN) }
    }

    companion object {
        private const val OWNER_KEY = "Владелец"
        private val HOST_RE = Regex("^[A-Za-z0-9.:-]{3,253}$")
        private val USER_RE = Regex("^[A-Za-z_][A-Za-z0-9_.-]{0,31}$")
        private val NAME_RE = Regex("^[\\p{L}\\p{N} _.-]{1,40}$")
    }
}
