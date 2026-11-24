package ru.usatyi.vpn

import android.content.Intent
import android.view.View
import android.widget.RadioButton
import android.widget.RadioGroup
import androidx.appcompat.app.AlertDialog
import androidx.core.widget.doAfterTextChanged
import ru.usatyi.vpn.databinding.DialogAddKeyBinding
import ru.usatyi.vpn.databinding.DialogKeySettingsBinding
import ru.usatyi.vpn.databinding.ItemKeyBinding
import ru.usatyi.vpn.databinding.ScreenConnectBinding

/**
 * Вкладка «Подключиться». Сверху — что сейчас поднято (и что будет при нажатии кнопки),
 * ниже — список ключей. У каждого ключа свои настройки: способ, маршрутизация, название.
 */
class ConnectScreen(private val act: MainActivity, private val b: ScreenConnectBinding) {

    private var keysSig = ""
    private var offeredClipboard: String? = null
    private var lastSystemCheck = 0L

    init {
        b.mainButton.setOnClickListener { onMainButton() }
        b.amneziaAddBtn.setOnClickListener { act.pickAmneziaFile() }
        b.amneziaRemoveBtn.setOnClickListener {
            Ui.confirm(act, "Убрать файл Amnezia с телефона? Сам сервер Amnezia не меняется.", "Убрать") {
                AmneziaStore.forget(act)
                ChainState.off()
                onState()
            }
        }
        b.addKeyBtn.setOnClickListener { showAddKeyDialog(null) }
        b.clipboardAddBtn.setOnClickListener {
            val text = offeredClipboard
            b.clipboardCard.visibility = View.GONE
            if (text != null) showAddKeyDialog(text)
        }
        b.clipboardHideBtn.setOnClickListener { b.clipboardCard.visibility = View.GONE }
        onState()
    }

    fun setVisible(v: Boolean) {
        b.root.visibility = if (v) View.VISIBLE else View.GONE
    }

    fun onResume() {
        offerClipboard()
        onState()
    }

    /** Вызывается при изменении состояния VPN и после действий пользователя. */
    fun onState() {
        renderKeys()
        renderStatus()
        renderAmnezia()
    }

    /** Блок Amnezia: что загружено и как это связано с цепочкой. */
    private fun renderAmnezia() {
        val cfg = AmneziaStore.load(act)
        val chain = ChainState.phase
        b.amneziaStatus.text = when {
            cfg == null -> "Файл .conf не загружен. Без него цепочка не подключится."
            chain == ChainState.Phase.RUNNING -> "Подключено через «${cfg.name}»: ${cfg.peer.endpoint}"
            else -> Amnezia.summary(cfg)
        }
        b.amneziaRemoveBtn.visibility = if (cfg == null) View.GONE else View.VISIBLE
        b.amneziaAddBtn.text = if (cfg == null) "Добавить Amnezia" else "Заменить файл .conf"
    }

    /** Раз в секунду: таймер и счётчики. Раз в две секунды: проверка, что VPN действительно поднят. */
    fun onTick() {
        renderStatus()
        val now = System.currentTimeMillis()
        if (now - lastSystemCheck >= 2000L) {
            lastSystemCheck = now
            checkSystemVpn()
        }
    }

    // ---------- карточка состояния

    private fun renderStatus() {
        val s = VpnState.current
        val selected = KeyStore.find(act, KeyStore.selectedId(act))
        val session = s.session
        val tone = when (s.phase) {
            VpnState.Phase.CONNECTED -> Tone.ACTIVE
            VpnState.Phase.CONNECTING -> Tone.WORKING
            VpnState.Phase.ERROR -> Tone.ERROR
            VpnState.Phase.IDLE -> Tone.NEUTRAL
        }
        b.statusCard.setCardBackgroundColor(Ui.containerColor(b.statusCard, tone))
        val on = Ui.onContainerColor(b.statusCard, tone)
        for (tv in listOf(b.statusTitle, b.statusKey, b.statusDetails, b.statusMessage, b.statusTraffic, b.statusQuota)) {
            tv.setTextColor(on)
        }

        b.statusTitle.text = when (s.phase) {
            VpnState.Phase.CONNECTED -> "Подключено"
            VpnState.Phase.CONNECTING -> "Подключаюсь…"
            VpnState.Phase.ERROR -> "Не получилось подключиться"
            VpnState.Phase.IDLE -> "Не подключено"
        }
        b.statusKey.text = when {
            session != null -> "Ключ «${session.keyName}» · сервер ${session.host}"
            selected != null -> "Выбран ключ «${selected.name}»"
            else -> "Ключ не выбран"
        }
        // во время подключения показываем сеанс; иначе — что будет использовано при следующем подключении
        val live = session != null && (s.phase == VpnState.Phase.CONNECTED || s.phase == VpnState.Phase.CONNECTING)
        val choice: Choice? = if (selected != null) {
            runCatching {
                val sv = selected.server
                Modes.choose(Modes.load(act), selected.transport, selected.routing) { sv.supports(it) }
            }.getOrNull()
        } else {
            null
        }
        b.statusDetails.text = when {
            session != null && live -> "Способ: ${session.transport.label} · ${session.routing.label}" + modeNote(session.mode)
            choice is Choice.Ok -> "Способ: ${choice.transport.label} · ${choice.routing.label}" + modeNote(choice.mode)
            choice is Choice.Error -> "Для этого ключа выбранный режим недоступен: ${choice.message}"
            session != null -> "Способ: ${session.transport.label} · ${session.routing.label}" + modeNote(session.mode)
            selected != null -> "Способ: ${selected.transport.label} · ${selected.routing.label}" + chainNote()
            else -> "Добавьте ключ, который вам прислали"
        }

        val chain = ChainState.phase
        if (chain == ChainState.Phase.RUNNING) {
            b.statusDetails.text = (b.statusDetails.text?.toString().orEmpty()) + "\nЧерез Amnezia: ${AmneziaStore.displayName(act)} → SSH"
        }
        // Пока цепочка поднимается, показываем её этап; ошибка цепочки важнее прочих сообщений.
        val msg = when {
            chain == ChainState.Phase.STARTING || chain == ChainState.Phase.WAIT_VPN -> ChainState.stage
            chain == ChainState.Phase.FAILED -> ChainState.error
            s.phase == VpnState.Phase.CONNECTED || s.message == "Не подключено" || s.message == "Отключено" -> ""
            else -> s.message
        }
        b.statusMessage.text = msg
        b.statusMessage.visibility = if (msg.isEmpty()) View.GONE else View.VISIBLE

        b.statusTraffic.text = if (s.phase == VpnState.Phase.CONNECTED) {
            val sec = (System.currentTimeMillis() - s.startedAt) / 1000L
            val chainLine = if (ChainState.phase == ChainState.Phase.RUNNING) {
                "\nAmneziaWG: ${Ui.bytes(ChainState.awgRx)} / ${Ui.bytes(ChainState.awgTx)}"
            } else {
                ""
            }
            "Сеанс ${Ui.duration(sec)} · получено ${Ui.bytes(s.rx)} · отправлено ${Ui.bytes(s.tx)}$chainLine"
        } else {
            ""
        }

        val used = s.used
        val limit = s.limit ?: 0L
        val quota = when {
            used != null -> "На сервере: ${Ui.bytes(used)} из ${limitText(limit)} · срок: ${dateText(s.expires ?: 0L)}"
            s.phase == VpnState.Phase.CONNECTED && session != null && !session.transport.isMtp ->
                "Остаток на сервере виден при подключении по MTP"
            s.phase == VpnState.Phase.CONNECTED -> "Жду данные сервера…"
            else -> ""
        }
        b.statusQuota.text = quota
        b.statusQuota.visibility = if (quota.isEmpty()) View.GONE else View.VISIBLE
        if (used != null && limit > 0L) {
            b.quotaBar.visibility = View.VISIBLE
            b.quotaBar.progress = (used * 100L / limit).coerceIn(0L, 100L).toInt()
        } else {
            b.quotaBar.visibility = View.GONE
        }

        // главная кнопка всегда говорит, что именно она сделает
        when (s.phase) {
            VpnState.Phase.CONNECTED -> {
                b.mainButton.text = "Отключить"
                b.mainButton.isEnabled = true
            }
            VpnState.Phase.CONNECTING -> {
                b.mainButton.text = "Отменить подключение"
                b.mainButton.isEnabled = true
            }
            else -> when {
                selected == null -> {
                    b.mainButton.text = "Сначала добавьте ключ"
                    b.mainButton.isEnabled = false
                }
                selected.transport == Transport.SS -> {
                    b.mainButton.text = "Скопировать ссылку ss:// для Shadowsocks"
                    b.mainButton.isEnabled = true
                }
                AmneziaStore.load(act) != null && selected.transport.isMtp -> {
                    b.mainButton.text = "Подключить через Amnezia «${AmneziaStore.displayName(act)}»"
                    b.mainButton.isEnabled = true
                }
                else -> {
                    b.mainButton.text = "Подключить «${selected.name}»"
                    b.mainButton.isEnabled = true
                }
            }
        }
    }

    /** Подсказка, пойдёт ли подключение цепочкой через Amnezia. */
    private fun chainNote(): String {
        val cfg = AmneziaStore.load(act) ?: return ""
        return "\nЦепочка: AmneziaWG «${cfg.name}» → SSH → MTP"
    }

    private fun onMainButton() {
        val s = VpnState.current
        if (s.phase == VpnState.Phase.CONNECTED || s.phase == VpnState.Phase.CONNECTING) {
            act.disconnect()
            return
        }
        val selected = KeyStore.find(act, KeyStore.selectedId(act))
        if (selected == null) act.toast("Сначала добавьте ключ") else act.connectKey(selected)
    }

    // ---------- список ключей

    private fun renderKeys() {
        val s = VpnState.current
        val keys = KeyStore.load(act)
        val selectedId = KeyStore.selectedId(act)
        val activeId = if (s.phase == VpnState.Phase.CONNECTED || s.phase == VpnState.Phase.CONNECTING) {
            s.session?.keyId
        } else {
            null
        }
        b.emptyKeys.visibility = if (keys.isEmpty()) View.VISIBLE else View.GONE
        val sig = StringBuilder().append(selectedId).append('|').append(activeId).append('|').append(s.phase.name)
        for (k in keys) {
            sig.append('|').append(k.id).append(k.name).append(k.transport.name).append(k.routing.name)
                .append(k.apps.sorted().joinToString(","))
        }
        val signature = sig.toString()
        if (signature == keysSig) return
        keysSig = signature
        b.keyList.removeAllViews()
        for (k in keys) {
            b.keyList.addView(buildRow(k, k.id == selectedId, activeId, s.phase))
        }
    }

    private fun buildRow(k: SavedKey, selected: Boolean, activeId: String?, phase: VpnState.Phase): View {
        val r = ItemKeyBinding.inflate(act.layoutInflater, b.keyList, false)
        val isActive = activeId == k.id
        val connected = isActive && phase == VpnState.Phase.CONNECTED
        val working = isActive && phase == VpnState.Phase.CONNECTING
        r.rowRadio.isChecked = selected
        r.rowName.text = k.name
        r.rowHost.text = try {
            "сервер ${k.server.host}"
        } catch (e: Exception) {
            "ключ повреждён"
        }
        r.rowMeta.text = "${k.transport.label} · ${k.routing.label}"
        r.rowBadge.text = if (connected) "подключён" else "подключаюсь…"
        r.rowBadge.visibility = if (connected || working) View.VISIBLE else View.GONE
        r.rowConnectBtn.text = when {
            connected -> "Отключить"
            working -> "Отменить"
            else -> "Подключить"
        }
        r.rowConnectBtn.setOnClickListener {
            if (connected || working) act.disconnect() else act.connectKey(k)
        }
        r.rowMenuBtn.setOnClickListener { showKeySettings(k) }
        r.rowCard.setOnClickListener {
            KeyStore.setSelected(act, k.id)
            onState()
        }
        r.rowCard.setStrokeWidth(if (selected) Ui.dp(act, 2) else 0)
        r.rowCard.setStrokeColor(Ui.attrColor(r.rowCard, androidx.appcompat.R.attr.colorPrimary))
        return r.root
    }

    // ---------- настройки ключа

    /** Окно настроек ключа: способ, маршрутизация, приложения, название, удаление. */
    fun showKeySettings(k: SavedKey) {
        val sk = try {
            k.server
        } catch (e: Exception) {
            act.toast("Ключ повреждён: удалите его и добавьте заново")
            return
        }
        val d = DialogKeySettingsBinding.inflate(act.layoutInflater)
        var transport = k.transport
        var routing = k.routing
        val apps = k.apps.toMutableSet()

        d.ksServer.text = "Сервер ${sk.host} · ключ «${k.name}»"
        val has = Transport.values().filter { sk.supports(it) }.joinToString(", ") { it.label }
        d.ksProtocols.text = "В ключе: ${if (has.isEmpty()) "ничего" else has}"
        d.ksAlias.setText(k.alias)
        d.ksAlias.hint = "название у вас: ${sk.name.ifEmpty { "без имени" }}"

        fun refreshApps() {
            d.ksApps.visibility = if (routing == Routing.APPS) View.VISIBLE else View.GONE
            d.ksApps.text = "Приложения через VPN: выбрано ${apps.size}"
        }

        fillRadio(
            d.ksTransportGroup,
            Transport.values().map { t ->
                Pair(t, "${t.label} · ${if (t.own) "свой протокол" else "стандарт"}\n${t.hint}")
            },
            transport,
            { sk.supports(it) },
        ) { transport = it }

        fillRadio(
            d.ksRoutingGroup,
            Routing.values().map { r -> Pair(r, "${r.label}\n${r.hint}") },
            routing,
            { true },
        ) {
            routing = it
            refreshApps()
        }
        refreshApps()

        d.ksApps.setOnClickListener {
            pickApps(apps) { chosen ->
                apps.clear()
                apps.addAll(chosen)
                refreshApps()
            }
        }
        d.ksCopySs.visibility = if (sk.ss != null) View.VISIBLE else View.GONE
        d.ksCopySs.setOnClickListener {
            val link = ssLink(sk)
            if (link != null) {
                act.copyText("ss-ссылка", link)
                act.toast("Ссылка ss:// скопирована")
            }
        }

        lateinit var dialog: AlertDialog
        d.ksDelete.setOnClickListener {
            Ui.confirm(act, "Удалить ключ «${k.name}» с этого телефона? На сервере он останется.", "Удалить") {
                KeyStore.remove(act, k.id)
                dialog.dismiss()
                onState()
            }
        }
        dialog = AlertDialog.Builder(act)
            .setTitle("Настройки ключа")
            .setView(d.root)
            .setPositiveButton("Сохранить") { _, _ ->
                KeyStore.update(
                    act,
                    k.copy(
                        alias = d.ksAlias.text.toString().trim(),
                        transport = transport,
                        routing = routing,
                        apps = apps.toSet(),
                    ),
                )
                act.toast("Настройки сохранены. Они применятся при следующем подключении")
                onState()
            }
            .setNegativeButton("Отмена", null)
            .create()
        dialog.show()
    }

    /** Группа переключателей с подписями. Пункт, которого нет в ключе, показывается, но недоступен. */
    @Suppress("UNCHECKED_CAST")
    private fun <T> fillRadio(
        group: RadioGroup,
        items: List<Pair<T, String>>,
        selected: T,
        enabled: (T) -> Boolean,
        onPick: (T) -> Unit,
    ) {
        group.removeAllViews()
        for ((value, text) in items) {
            val rb = RadioButton(act)
            rb.id = View.generateViewId()
            rb.tag = value
            rb.text = text
            rb.isEnabled = enabled(value)
            rb.isChecked = value == selected
            group.addView(rb)
        }
        group.setOnCheckedChangeListener { g, checkedId ->
            val picked = g.findViewById<RadioButton>(checkedId)?.tag
            if (picked != null) onPick(picked as T)
        }
    }

    /** Список приложений с иконкой запуска: выбираем, какие пойдут через VPN. */
    private fun pickApps(selected: Set<String>, onDone: (Set<String>) -> Unit) {
        Thread {
            val pm = act.packageManager
            val launcher = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
            val list = pm.queryIntentActivities(launcher, 0)
                .map { Pair(it.activityInfo.packageName, it.loadLabel(pm).toString()) }
                .filter { it.first != act.packageName }
                .distinctBy { it.first }
                .sortedBy { it.second.lowercase() }
            val checked = BooleanArray(list.size) { selected.contains(list[it].first) }
            act.runOnUiThread {
                AlertDialog.Builder(act)
                    .setTitle("Какие приложения пойдут через VPN")
                    .setMultiChoiceItems(list.map { it.second }.toTypedArray(), checked) { _, which, isChecked ->
                        checked[which] = isChecked
                    }
                    .setPositiveButton("Готово") { _, _ ->
                        onDone(list.filterIndexed { i, _ -> checked[i] }.map { it.first }.toSet())
                    }
                    .setNegativeButton("Отмена", null)
                    .show()
            }
        }.start()
    }

    // ---------- добавление ключа

    /** Окно добавления: вставьте строку mtp://, сразу видно, что в ключе, и можно добавить. */
    fun showAddKeyDialog(prefill: String?) {
        val d = DialogAddKeyBinding.inflate(act.layoutInflater)
        if (!prefill.isNullOrBlank()) d.akInput.setText(prefill)
        val dialog = AlertDialog.Builder(act)
            .setTitle("Добавить ключ")
            .setView(d.root)
            .setPositiveButton("Добавить", null)
            .setNegativeButton("Отмена", null)
            .create()
        d.akPaste.setOnClickListener {
            val text = act.clipboardText()
            if (text == null) act.toast("В буфере обмена нет текста") else d.akInput.setText(text)
        }
        d.akInput.doAfterTextChanged { checkKeyInput(d, dialog, it?.toString().orEmpty()) }
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val text = d.akInput.text.toString().trim()
                try {
                    val r = KeyStore.addOrUpdate(act, text)
                    KeyStore.setSelected(act, r.key.id)
                    act.toast(
                        when {
                            r.created -> "Ключ «${r.key.name}» добавлен"
                            r.replaced -> "Ключ «${r.key.name}» обновлён"
                            else -> "Этот ключ уже есть в списке"
                        },
                    )
                    dialog.dismiss()
                    keysSig = ""
                    onState()
                } catch (e: Exception) {
                    d.akPreview.text = "Не получилось добавить: ${e.message}"
                }
            }
            checkKeyInput(d, dialog, d.akInput.text.toString())
        }
        dialog.show()
    }

    private fun checkKeyInput(d: DialogAddKeyBinding, dialog: AlertDialog, raw: String) {
        val text = raw.trim()
        var ok = false
        if (text.isEmpty()) {
            d.akPreview.text = "Вставьте строку, которая начинается с mtp://"
        } else {
            try {
                val k = KeyCodec.decode(text)
                val existing = KeyStore.load(act).firstOrNull { it.id == k.uid.toHex() }
                val ways = Transport.values().filter { k.supports(it) }.joinToString(", ") { it.label }
                val lines = StringBuilder()
                lines.append("Имя: ").append(k.name.ifEmpty { "без имени" }).append('\n')
                lines.append("Сервер: ").append(k.host).append('\n')
                lines.append("Способы: ").append(ways)
                if (existing != null) {
                    lines.append('\n')
                    if (existing.text == text) {
                        lines.append("Такой ключ уже есть в списке")
                    } else {
                        lines.append("Ключ уже есть: строка обновится, настройки сохранятся")
                        ok = true
                    }
                } else {
                    ok = true
                }
                d.akPreview.text = lines.toString()
            } catch (e: Exception) {
                d.akPreview.text = "Это не ключ mtp://…"
            }
        }
        dialog.getButton(AlertDialog.BUTTON_POSITIVE)?.isEnabled = ok
    }

    /** Если в буфере обмена есть новый ключ, предлагаем добавить его одним нажатием. */
    private fun offerClipboard() {
        val text = act.clipboardText()
        if (text == null || !text.startsWith("mtp://") || text == offeredClipboard) return
        val k = try {
            KeyCodec.decode(text)
        } catch (e: Exception) {
            return
        }
        if (KeyStore.load(act).any { it.id == k.uid.toHex() && it.text == text }) return
        offeredClipboard = text
        b.clipboardText.text = "В буфере обмена ключ «${k.name.ifEmpty { "без имени" }}» (сервер ${k.host}). Добавить в список?"
        b.clipboardCard.visibility = View.VISIBLE
    }

    // ---------- проверка связи с системой

    /**
     * Если приложение думает, что VPN поднят, а система его уже выключила (например, в настройках Android),
     * переводим экран в «не подключено», чтобы не вводить в заблуждение.
     */
    private fun checkSystemVpn() {
        val s = VpnState.current
        val session = s.session ?: return
        if (s.phase != VpnState.Phase.CONNECTED) return
        Thread {
            if (session.transport == Transport.WG) {
                if (WgController.isUp()) {
                    WgController.traffic()?.let { (rx, tx) -> VpnState.traffic(rx, tx) }
                } else {
                    VpnState.idle("VPN выключен в системе")
                }
            } else if (System.currentTimeMillis() - s.startedAt > 8000L && !act.vpnTransportPresent()) {
                VpnState.idle("VPN выключен в системе")
            }
        }.start()
    }
}

/** Подпись режима для карточки состояния: обычный режим ничего не добавляет. */
private fun modeNote(m: Mode): String = if (m == Mode.NORMAL) "" else " · ${m.label.lowercase()} режим"
