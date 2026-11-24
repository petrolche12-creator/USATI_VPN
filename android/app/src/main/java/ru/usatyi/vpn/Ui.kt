package ru.usatyi.vpn

import android.app.Activity
import android.content.Context
import android.view.View
import androidx.appcompat.app.AlertDialog
import com.google.android.material.color.MaterialColors
import java.util.Locale

/** Смысловой тон карточки: нейтральный, в процессе, подключено, ошибка. */
enum class Tone { NEUTRAL, WORKING, ACTIVE, ERROR }

/** Общие помощники интерфейса: цвета, размеры, подписи, подтверждения. */
object Ui {
    fun dp(c: Context, v: Int): Int = (v * c.resources.displayMetrics.density).toInt()

    /** Цвет атрибута темы (0, если атрибута нет). */
    fun attrColor(view: View, attr: Int): Int = MaterialColors.getColor(view, attr, 0)

    fun containerColor(view: View, tone: Tone): Int = attrColor(
        view,
        when (tone) {
            Tone.NEUTRAL -> com.google.android.material.R.attr.colorSurfaceVariant
            Tone.WORKING -> com.google.android.material.R.attr.colorSecondaryContainer
            Tone.ACTIVE -> com.google.android.material.R.attr.colorPrimaryContainer
            Tone.ERROR -> com.google.android.material.R.attr.colorErrorContainer
        },
    )

    fun onContainerColor(view: View, tone: Tone): Int = attrColor(
        view,
        when (tone) {
            Tone.NEUTRAL -> com.google.android.material.R.attr.colorOnSurfaceVariant
            Tone.WORKING -> com.google.android.material.R.attr.colorOnSecondaryContainer
            Tone.ACTIVE -> com.google.android.material.R.attr.colorOnPrimaryContainer
            Tone.ERROR -> com.google.android.material.R.attr.colorOnErrorContainer
        },
    )

    /** Размер в понятных единицах: байты, КБ, МБ, ГБ. */
    fun bytes(n: Long): String = when {
        n < 1024L -> "$n Б"
        n < 1024L * 1024 -> String.format(Locale.US, "%.1f КБ", n / 1024.0)
        n < 1024L * 1024 * 1024 -> String.format(Locale.US, "%.1f МБ", n / (1024.0 * 1024))
        else -> String.format(Locale.US, "%.2f ГБ", n / (1024.0 * 1024 * 1024))
    }

    /** Длительность сеанса: часы:минуты:секунды. */
    fun duration(sec: Long): String =
        String.format(Locale.US, "%d:%02d:%02d", sec / 3600, (sec / 60) % 60, sec % 60)

    /** Диалог подтверждения: действие необратимо или меняет чужой доступ. */
    fun confirm(act: Activity, message: String, action: String, onYes: () -> Unit) {
        AlertDialog.Builder(act)
            .setMessage(message)
            .setPositiveButton(action) { _, _ -> onYes() }
            .setNegativeButton("Отмена", null)
            .show()
    }
}
