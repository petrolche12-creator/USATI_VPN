// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.EditText
import android.widget.TextView

/** dialog_add_key.xml */
class DialogAddKeyBinding(val root: android.view.View) {
    val akInput: EditText = root.findViewById(R.id.akInput)
    val akPaste: Button = root.findViewById(R.id.akPaste)
    val akPreview: TextView = root.findViewById(R.id.akPreview)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogAddKeyBinding =
            DialogAddKeyBinding(inflater.inflate(R.layout.dialog_add_key, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogAddKeyBinding =
            DialogAddKeyBinding(inflater.inflate(R.layout.dialog_add_key, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogAddKeyBinding =
            DialogAddKeyBinding(inflater.inflate(R.layout.dialog_add_key, parent, attach))
    }
}
