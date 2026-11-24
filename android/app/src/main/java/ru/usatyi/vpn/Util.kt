package ru.usatyi.vpn

import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** Срок ключа по unix-времени или «без срока». */
internal fun dateText(epochSec: Long): String =
    if (epochSec <= 0L) {
        "без срока"
    } else {
        SimpleDateFormat("dd.MM.yyyy", Locale.getDefault()).format(Date(epochSec * 1000L))
    }

/** Лимит трафика: «без лимита» или размер. */
internal fun limitText(limit: Long): String = if (limit <= 0L) "без лимита" else Ui.bytes(limit)

internal fun numText(d: Double): String =
    if (d == Math.floor(d)) d.toLong().toString() else String.format(Locale.US, "%.2f", d)
