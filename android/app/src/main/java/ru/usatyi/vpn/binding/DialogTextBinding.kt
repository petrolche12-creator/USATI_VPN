// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.ScrollView
import android.widget.TextView

/** dialog_text.xml */
class DialogTextBinding(val root: android.view.View) {
    val dtScroll: ScrollView = root.findViewById(R.id.dtScroll)
    val dtText: TextView = root.findViewById(R.id.dtText)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogTextBinding =
            DialogTextBinding(inflater.inflate(R.layout.dialog_text, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogTextBinding =
            DialogTextBinding(inflater.inflate(R.layout.dialog_text, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogTextBinding =
            DialogTextBinding(inflater.inflate(R.layout.dialog_text, parent, attach))
    }
}
