// Сгенерировано tools/gen_bindings.py — не редактировать вручную.
package ru.usatyi.vpn.binding

import ru.usatyi.vpn.R

import android.widget.FrameLayout
import ru.usatyi.vpn.BottomBar

/** activity_main.xml */
class ActivityMainBinding(val root: android.view.View) {
    val bottomNav: BottomBar = root.findViewById(R.id.bottomNav)
    val container: FrameLayout = root.findViewById(R.id.container)

    companion object {
        fun inflate(inflater: android.view.LayoutInflater): ActivityMainBinding =
            ActivityMainBinding(inflater.inflate(R.layout.activity_main, null, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup): ActivityMainBinding =
            ActivityMainBinding(inflater.inflate(R.layout.activity_main, parent, false))

        fun inflate(inflater: android.view.LayoutInflater, parent: android.view.ViewGroup, attach: Boolean): ActivityMainBinding =
            ActivityMainBinding(inflater.inflate(R.layout.activity_main, parent, attach))
    }
}
