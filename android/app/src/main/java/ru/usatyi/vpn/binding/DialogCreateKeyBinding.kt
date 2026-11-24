// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.EditText

/** dialog_create_key.xml */
class DialogCreateKeyBinding(val root: android.view.View) {
    val ckDays: EditText = root.findViewById(R.id.ckDays)
    val ckLimit: EditText = root.findViewById(R.id.ckLimit)
    val ckName: EditText = root.findViewById(R.id.ckName)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogCreateKeyBinding =
            DialogCreateKeyBinding(inflater.inflate(R.layout.dialog_create_key, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogCreateKeyBinding =
            DialogCreateKeyBinding(inflater.inflate(R.layout.dialog_create_key, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogCreateKeyBinding =
            DialogCreateKeyBinding(inflater.inflate(R.layout.dialog_create_key, parent, attach))
    }
}
