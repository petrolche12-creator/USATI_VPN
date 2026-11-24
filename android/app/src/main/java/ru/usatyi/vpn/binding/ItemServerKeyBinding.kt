// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.TextView
import ru.usatyi.vpn.CardView

/** item_server_key.xml */
class ItemServerKeyBinding(val root: android.view.View) {
    val skActionsBtn: Button = root.findViewById(R.id.skActionsBtn)
    val skCard: CardView = root.findViewById(R.id.skCard)
    val skName: TextView = root.findViewById(R.id.skName)
    val skOnline: TextView = root.findViewById(R.id.skOnline)
    val skStatus: TextView = root.findViewById(R.id.skStatus)
    val skTraffic: TextView = root.findViewById(R.id.skTraffic)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ItemServerKeyBinding =
            ItemServerKeyBinding(inflater.inflate(R.layout.item_server_key, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ItemServerKeyBinding =
            ItemServerKeyBinding(inflater.inflate(R.layout.item_server_key, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ItemServerKeyBinding =
            ItemServerKeyBinding(inflater.inflate(R.layout.item_server_key, parent, attach))
    }
}
