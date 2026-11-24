// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.TextView

/** dialog_key_result.xml */
class DialogKeyResultBinding(val root: android.view.View) {
    val dkAddSelf: Button = root.findViewById(R.id.dkAddSelf)
    val dkClose: Button = root.findViewById(R.id.dkClose)
    val dkCopy: Button = root.findViewById(R.id.dkCopy)
    val dkShare: Button = root.findViewById(R.id.dkShare)
    val dkText: TextView = root.findViewById(R.id.dkText)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogKeyResultBinding =
            DialogKeyResultBinding(inflater.inflate(R.layout.dialog_key_result, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogKeyResultBinding =
            DialogKeyResultBinding(inflater.inflate(R.layout.dialog_key_result, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogKeyResultBinding =
            DialogKeyResultBinding(inflater.inflate(R.layout.dialog_key_result, parent, attach))
    }
}
