package ru.usatyi.vpn

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject

/** Ключ, сохранённый на телефоне, со своими настройками: название, способ, маршрутизация, приложения. */
data class SavedKey(
    val id: String,
    val text: String,
    val alias: String,
    val transport: Transport,
    val routing: Routing,
    val apps: Set<String>,
) {
    val server: ServerKey get() = KeyCodec.decode(text)

    /** Название для показа: своё, если задано, иначе имя из ключа. */
    val name: String get() = alias.ifEmpty { server.name.ifEmpty { "Без имени" } }
}

/** Результат добавления: новый ключ, обновлённая строка или уже известный ключ. */
class AddResult(val key: SavedKey, val created: Boolean, val replaced: Boolean)

/** Список ключей на телефоне и выбранный ключ. Хранится в SharedPreferences в виде JSON. */
object KeyStore {
    private const val PREFS = "usatyi_keys"
    private const val OLD_PREFS = "usatyi_vpn"

    fun load(c: Context): List<SavedKey> {
        val raw = prefs(c).getString("list", null)
        if (raw != null) return fromJson(raw)
        return migrateOld(c)
    }

    fun find(c: Context, id: String?): SavedKey? = if (id == null) null else load(c).firstOrNull { it.id == id }

    fun selectedId(c: Context): String? = prefs(c).getString("selected", null)

    fun setSelected(c: Context, id: String?) {
        prefs(c).edit().putString("selected", id).apply()
    }

    /** Добавляет ключ. Если такой ключ уже есть, заменяет строку и сохраняет его настройки. */
    fun addOrUpdate(c: Context, text: String): AddResult {
        val clean = text.trim()
        val server = KeyCodec.decode(clean) // бросит исключение для неверной строки
        val id = server.uid.toHex()
        val list = load(c).toMutableList()
        val idx = list.indexOfFirst { it.id == id }
        val result = if (idx >= 0) {
            val old = list[idx]
            if (old.text == clean) {
                AddResult(old, created = false, replaced = false)
            } else {
                val updated = old.copy(text = clean)
                list[idx] = updated
                AddResult(updated, created = false, replaced = true)
            }
        } else {
            val fresh = SavedKey(id, clean, "", defaultTransport(server), Routing.ALL, emptySet())
            list.add(fresh)
            AddResult(fresh, created = true, replaced = false)
        }
        save(c, list)
        val sel = selectedId(c)
        if (sel == null || find(c, sel) == null) setSelected(c, result.key.id)
        return result
    }

    fun update(c: Context, key: SavedKey) {
        val list = load(c).toMutableList()
        val idx = list.indexOfFirst { it.id == key.id }
        if (idx >= 0) list[idx] = key else list.add(key)
        save(c, list)
    }

    fun remove(c: Context, id: String) {
        save(c, load(c).filter { it.id != id })
        if (selectedId(c) == id) setSelected(c, null)
    }

    /** Первый способ, который есть в ключе: сначала свой MTP/1, потом остальные. */
    fun defaultTransport(key: ServerKey): Transport =
        Transport.values().firstOrNull { key.supports(it) } ?: Transport.MTP1

    fun toJson(list: List<SavedKey>): String {
        val arr = JSONArray()
        for (k in list) {
            val apps = JSONArray()
            for (a in k.apps.sorted()) apps.put(a)
            arr.put(
                JSONObject()
                    .put("id", k.id)
                    .put("text", k.text)
                    .put("alias", k.alias)
                    .put("transport", k.transport.name)
                    .put("routing", k.routing.name)
                    .put("apps", apps),
            )
        }
        return arr.toString()
    }

    /** Читает список; испорченные записи пропускает. */
    fun fromJson(raw: String): List<SavedKey> {
        val out = mutableListOf<SavedKey>()
        val arr = try {
            JSONArray(raw)
        } catch (e: Exception) {
            return emptyList()
        }
        for (i in 0 until arr.length()) {
            val o = arr.optJSONObject(i) ?: continue
            val text = o.optString("text")
            val server = try {
                KeyCodec.decode(text)
            } catch (e: Exception) {
                continue
            }
            val appsArr = o.optJSONArray("apps")
            val apps = if (appsArr == null) emptySet() else (0 until appsArr.length()).map { appsArr.getString(it) }.toSet()
            out.add(
                SavedKey(
                    id = o.optString("id", server.uid.toHex()),
                    text = text,
                    alias = o.optString("alias"),
                    transport = Transport.values().firstOrNull { it.name == o.optString("transport") }
                        ?: defaultTransport(server),
                    routing = Routing.values().firstOrNull { it.name == o.optString("routing") } ?: Routing.ALL,
                    apps = apps,
                ),
            )
        }
        return out
    }

    private fun save(c: Context, list: List<SavedKey>) {
        prefs(c).edit().putString("list", toJson(list)).apply()
    }

    /** Перенос ключа из версии с одним ключом (настройки лежали в usatyi_vpn). */
    private fun migrateOld(c: Context): List<SavedKey> {
        val old = c.getSharedPreferences(OLD_PREFS, Context.MODE_PRIVATE)
        val oldKey = old.getString("active_key", null) ?: return emptyList()
        val parsed = try {
            KeyCodec.decode(oldKey)
        } catch (e: Exception) {
            return emptyList()
        }
        val transport = Transport.values().firstOrNull { it.name == old.getString("transport", null) }
            ?: defaultTransport(parsed)
        val routing = Routing.values().firstOrNull { it.name == old.getString("routing", null) } ?: Routing.ALL
        val apps = old.getStringSet("apps", emptySet())?.toSet().orEmpty()
        val migrated = SavedKey(parsed.uid.toHex(), oldKey, "", transport, routing, apps)
        save(c, listOf(migrated))
        setSelected(c, migrated.id)
        return listOf(migrated)
    }

    private fun prefs(c: Context) = c.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
