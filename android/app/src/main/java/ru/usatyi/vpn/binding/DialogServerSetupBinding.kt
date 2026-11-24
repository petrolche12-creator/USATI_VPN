// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.EditText

/** dialog_server_setup.xml */
class DialogServerSetupBinding(val root: android.view.View) {
    val ssHost: EditText = root.findViewById(R.id.ssHost)
    val ssPass: EditText = root.findViewById(R.id.ssPass)
    val ssSshPort: EditText = root.findViewById(R.id.ssSshPort)
    val ssUser: EditText = root.findViewById(R.id.ssUser)
    val ssVpnPort: EditText = root.findViewById(R.id.ssVpnPort)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogServerSetupBinding =
            DialogServerSetupBinding(inflater.inflate(R.layout.dialog_server_setup, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogServerSetupBinding =
            DialogServerSetupBinding(inflater.inflate(R.layout.dialog_server_setup, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogServerSetupBinding =
            DialogServerSetupBinding(inflater.inflate(R.layout.dialog_server_setup, parent, attach))
    }
}
