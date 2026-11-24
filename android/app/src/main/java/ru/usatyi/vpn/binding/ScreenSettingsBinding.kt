// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.RadioGroup
import android.widget.Switch
import android.widget.TextView

/** screen_settings.xml */
class ScreenSettingsBinding(val root: android.view.View) {
    val advSwitch: Switch = root.findViewById(R.id.advSwitch)
    val advText: TextView = root.findViewById(R.id.advText)
    val modeGroup: RadioGroup = root.findViewById(R.id.modeGroup)
    val modeNotice: TextView = root.findViewById(R.id.modeNotice)
    val protoGroup: RadioGroup = root.findViewById(R.id.protoGroup)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ScreenSettingsBinding =
            ScreenSettingsBinding(inflater.inflate(R.layout.screen_settings, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ScreenSettingsBinding =
            ScreenSettingsBinding(inflater.inflate(R.layout.screen_settings, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ScreenSettingsBinding =
            ScreenSettingsBinding(inflater.inflate(R.layout.screen_settings, parent, attach))
    }
}
