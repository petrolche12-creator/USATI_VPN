package ru.usatyi.vpn

import android.content.Context
import org.json.JSONObject

/** Данные своего сервера на телефоне: SSH-доступ и порт VPN. Ключи хранятся в KeyStore. */
object Settings {
    private const val PREFS = "usatyi_vpn"

    private fun prefs(c: Context) = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)

    fun owner(c: Context): Owner? {
        val raw = prefs(c).getString("owner", null) ?: return null
        return try {
            val j = JSONObject(raw)
            Owner(
                host = j.getString("host"),
                sshPort = j.optInt("sshPort", 22),
                sshUser = j.optString("sshUser", "root"),
                sshPass = j.optString("sshPass", ""),
                vpnPort = j.optInt("vpnPort", 443),
                isRoot = j.optBoolean("isRoot", false),
                hostFp = j.optString("hostFp", ""),
            )
        } catch (e: Exception) {
            null
        }
    }

    fun saveOwner(c: Context, o: Owner) {
        val j = JSONObject()
            .put("host", o.host)
            .put("sshPort", o.sshPort)
            .put("sshUser", o.sshUser)
            .put("sshPass", o.sshPass)
            .put("vpnPort", o.vpnPort)
            .put("isRoot", o.isRoot)
            .put("hostFp", o.hostFp)
        prefs(c).edit().putString("owner", j.toString()).apply()
    }

    fun clearOwner(c: Context) {
        prefs(c).edit().remove("owner").apply()
    }
}
