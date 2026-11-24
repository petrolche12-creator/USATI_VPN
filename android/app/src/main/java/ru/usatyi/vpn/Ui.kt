package ru.usatyi.vpn

import android.app.Activity
import android.content.Context
import android.view.View
import java.util.Locale

/** Смысловой тон карточки: нейтральный, в процессе, подключено, ошибка. */
enum class Tone { NEUTRAL, WORKING, ACTIVE, ERROR }

/** Общие помощники интерфейса: цвета, размеры, подписи, подтверждения. */
object Ui {
    fun dp(c: Context, v: Int): Int = (v * c.resources.displayMetrics.density).toInt()

    /** Цвет из темы приложения (свои цвета вместо библиотеки Material). */
    fun accent(): Int = Theme.primary

    fun containerColor(view: View, tone: Tone): Int = Colors.container(tone)

    fun onContainerColor(view: View, tone: Tone): Int = Colors.onContainer(tone)

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
        DialogBuilder(act)
            .setMessage(message)
            .setPositiveButton(action) { _, _ -> onYes() }
            .setNegativeButton("Отмена", null)
            .show()
    }
}
