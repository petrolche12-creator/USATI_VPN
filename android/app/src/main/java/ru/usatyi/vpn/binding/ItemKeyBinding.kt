// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.view.View
import android.widget.Button
import android.widget.RadioButton
import android.widget.TextView
import ru.usatyi.vpn.CardView

/** item_key.xml */
class ItemKeyBinding(val root: android.view.View) {
    val rowBadge: TextView = root.findViewById(R.id.rowBadge)
    val rowCard: CardView = root.findViewById(R.id.rowCard)
    val rowConnectBtn: Button = root.findViewById(R.id.rowConnectBtn)
    val rowHost: TextView = root.findViewById(R.id.rowHost)
    val rowMenuBtn: View = root.findViewById(R.id.rowMenuBtn)
    val rowMeta: TextView = root.findViewById(R.id.rowMeta)
    val rowName: TextView = root.findViewById(R.id.rowName)
    val rowRadio: RadioButton = root.findViewById(R.id.rowRadio)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ItemKeyBinding =
            ItemKeyBinding(inflater.inflate(R.layout.item_key, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ItemKeyBinding =
            ItemKeyBinding(inflater.inflate(R.layout.item_key, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ItemKeyBinding =
            ItemKeyBinding(inflater.inflate(R.layout.item_key, parent, attach))
    }
}
