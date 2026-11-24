// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import ru.usatyi.vpn.CardView
import ru.usatyi.vpn.ProgressLine

/** screen_connect.xml */
class ScreenConnectBinding(val root: android.view.View) {
    val addKeyBtn: Button = root.findViewById(R.id.addKeyBtn)
    val amneziaAddBtn: Button = root.findViewById(R.id.amneziaAddBtn)
    val amneziaCard: CardView = root.findViewById(R.id.amneziaCard)
    val amneziaRemoveBtn: Button = root.findViewById(R.id.amneziaRemoveBtn)
    val amneziaStatus: TextView = root.findViewById(R.id.amneziaStatus)
    val clipboardAddBtn: Button = root.findViewById(R.id.clipboardAddBtn)
    val clipboardCard: CardView = root.findViewById(R.id.clipboardCard)
    val clipboardHideBtn: Button = root.findViewById(R.id.clipboardHideBtn)
    val clipboardText: TextView = root.findViewById(R.id.clipboardText)
    val emptyKeys: TextView = root.findViewById(R.id.emptyKeys)
    val keyList: LinearLayout = root.findViewById(R.id.keyList)
    val mainButton: Button = root.findViewById(R.id.mainButton)
    val quotaBar: ProgressLine = root.findViewById(R.id.quotaBar)
    val statusCard: CardView = root.findViewById(R.id.statusCard)
    val statusDetails: TextView = root.findViewById(R.id.statusDetails)
    val statusKey: TextView = root.findViewById(R.id.statusKey)
    val statusMessage: TextView = root.findViewById(R.id.statusMessage)
    val statusQuota: TextView = root.findViewById(R.id.statusQuota)
    val statusTitle: TextView = root.findViewById(R.id.statusTitle)
    val statusTraffic: TextView = root.findViewById(R.id.statusTraffic)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ScreenConnectBinding =
            ScreenConnectBinding(inflater.inflate(R.layout.screen_connect, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ScreenConnectBinding =
            ScreenConnectBinding(inflater.inflate(R.layout.screen_connect, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ScreenConnectBinding =
            ScreenConnectBinding(inflater.inflate(R.layout.screen_connect, parent, attach))
    }
}
