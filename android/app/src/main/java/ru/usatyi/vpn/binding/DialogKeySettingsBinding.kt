// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.Button
import android.widget.EditText
import android.widget.RadioGroup
import android.widget.TextView

/** dialog_key_settings.xml */
class DialogKeySettingsBinding(val root: android.view.View) {
    val ksAlias: EditText = root.findViewById(R.id.ksAlias)
    val ksApps: Button = root.findViewById(R.id.ksApps)
    val ksCopySs: Button = root.findViewById(R.id.ksCopySs)
    val ksDelete: Button = root.findViewById(R.id.ksDelete)
    val ksProtocols: TextView = root.findViewById(R.id.ksProtocols)
    val ksRoutingGroup: RadioGroup = root.findViewById(R.id.ksRoutingGroup)
    val ksServer: TextView = root.findViewById(R.id.ksServer)
    val ksTransportGroup: RadioGroup = root.findViewById(R.id.ksTransportGroup)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): DialogKeySettingsBinding =
            DialogKeySettingsBinding(inflater.inflate(R.layout.dialog_key_settings, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): DialogKeySettingsBinding =
            DialogKeySettingsBinding(inflater.inflate(R.layout.dialog_key_settings, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): DialogKeySettingsBinding =
            DialogKeySettingsBinding(inflater.inflate(R.layout.dialog_key_settings, parent, attach))
    }
}
