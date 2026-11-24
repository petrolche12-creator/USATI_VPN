// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import ru.usatyi.vpn.CardView

/** screen_create.xml */
class ScreenCreateBinding(val root: android.view.View) {
    val createKeyBtn: Button = root.findViewById(R.id.createKeyBtn)
    val serverCard: CardView = root.findViewById(R.id.serverCard)
    val serverCheckBtn: Button = root.findViewById(R.id.serverCheckBtn)
    val serverForgetBtn: Button = root.findViewById(R.id.serverForgetBtn)
    val serverKeyList: LinearLayout = root.findViewById(R.id.serverKeyList)
    val serverKeysEmpty: TextView = root.findViewById(R.id.serverKeysEmpty)
    val serverSetupBtn: Button = root.findViewById(R.id.serverSetupBtn)
    val serverStatus: TextView = root.findViewById(R.id.serverStatus)
    val serverTitle: TextView = root.findViewById(R.id.serverTitle)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ScreenCreateBinding =
            ScreenCreateBinding(inflater.inflate(R.layout.screen_create, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ScreenCreateBinding =
            ScreenCreateBinding(inflater.inflate(R.layout.screen_create, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ScreenCreateBinding =
            ScreenCreateBinding(inflater.inflate(R.layout.screen_create, parent, attach))
    }
}
