package ru.usatyi.vpn

import android.text.SpannableStringBuilder
import android.text.Spanned
import android.text.style.RelativeSizeSpan
import android.view.View
import android.view.ViewGroup
import android.widget.RadioButton
import android.widget.RadioGroup
import ru.usatyi.vpn.databinding.ScreenSettingsBinding

/**
 * Вкладка «Настройки». Режим выбирается всегда. Ручной выбор протокола выключен по умолчанию.
 * Всё, что выбрано здесь, применяется при следующем подключении.
 */
class SettingsScreen(private val act: MainActivity, private val b: ScreenSettingsBinding) {

    private var modes: AppModes = Modes.load(act)

    /** Пока мы сами расставляем отметки, обратные вызовы игнорируем. */
    private var drawing = false

    private val modeButtons = LinkedHashMap<Mode, RadioButton>()
    private val protoButtons = LinkedHashMap<ProtoChoice, RadioButton>()

    init {
        for (m in Mode.values()) {
            val rb = option(m.label, m.hint)
            modeButtons[m] = rb
            b.modeGroup.addView(rb)
        }
        for (p in ProtoChoice.values()) {
            val rb = option(p.label, p.transport?.hint ?: AUTO_HINT)
            protoButtons[p] = rb
            b.protoGroup.addView(rb)
        }
        b.modeGroup.setOnCheckedChangeListener { _, id ->
            if (!drawing) modeButtons.entries.firstOrNull { it.value.id == id }?.let { save(modes.copy(mode = it.key)) }
        }
        b.protoGroup.setOnCheckedChangeListener { _, id ->
            if (!drawing) protoButtons.entries.firstOrNull { it.value.id == id }?.let { save(modes.copy(manual = true, proto = it.key)) }
        }
        b.advSwitch.setOnCheckedChangeListener { _, checked ->
            if (!drawing) save(modes.copy(manual = checked))
        }
        draw()
    }

    fun setVisible(v: Boolean) {
        b.root.visibility = if (v) View.VISIBLE else View.GONE
    }

    fun onShown() {
        modes = Modes.load(act)
        draw()
    }

    private fun save(m: AppModes) {
        modes = m
        Modes.save(act, m)
        draw()
        act.toast("Сохранено. Применится при следующем подключении")
    }

    private fun draw() {
        drawing = true
        try {
            b.advSwitch.isChecked = modes.manual
            b.modeGroup.check(modeButtons.getValue(modes.mode).id)
            b.protoGroup.check(protoButtons.getValue(if (modes.manual) modes.proto else ProtoChoice.AUTO).id)
            b.protoGroup.visibility = if (modes.manual) View.VISIBLE else View.GONE
            b.advText.text = protocolText()
            val phase = VpnState.current.phase
            val busy = phase == VpnState.Phase.CONNECTED || phase == VpnState.Phase.CONNECTING
            b.modeNotice.visibility = if (busy) View.VISIBLE else View.GONE
        } finally {
            drawing = false
        }
    }

    private fun protocolText(): String = when {
        !modes.manual ->
            "Сейчас протокол выбирает режим: ${MODE_PROTOCOL.getValue(modes.mode)}. Чтобы выбрать протокол самому, включите переключатель ниже."
        modes.proto == ProtoChoice.AUTO ->
            "Протокол выбирает режим: ${MODE_PROTOCOL.getValue(modes.mode)}."
        else ->
            "Выбран вручную: ${modes.proto.label}. Работает для всех ключей, где этот способ есть."
    }

    private fun option(title: String, hint: String): RadioButton {
        val label = SpannableStringBuilder(title).append('\n').append(hint)
        label.setSpan(RelativeSizeSpan(0.88f), title.length + 1, label.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
        return RadioButton(act).apply {
            id = View.generateViewId()
            text = label
            layoutParams = RadioGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
            ).apply { topMargin = Ui.dp(act, 8) }
        }
    }

    companion object {
        private const val AUTO_HINT = "игровой: UDP, анонимный: MTP/3, обычный: как в ключе"
        private val MODE_PROTOCOL = mapOf(
            Mode.GAME to "UDP (MTP/1, иначе WireGuard)",
            Mode.NORMAL to "как в ключе",
            Mode.ANON to "MTP/3 (маскировка под TLS)",
        )
    }
}
