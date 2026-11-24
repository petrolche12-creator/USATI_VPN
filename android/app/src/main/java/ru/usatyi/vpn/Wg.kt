package ru.usatyi.vpn

/**
 * Режим «WireGuard напрямую» в этой сборке отключён.
 *
 * Раньше он поднимался библиотекой com.wireguard.android:tunnel, но она требует сборки с
 * Gradle и Maven-репозиториев. Эта сборка APK делается без внешних библиотек (см.
 * tools/build-apk.sh), поэтому для ключей без Amnezia остаются свои способы MTP/1–3,
 * а с загруженным .conf — цепочка AmneziaWG → SSH → MTP.
 *
 * Заглушки ниже сохраняют вызовы в остальном коде: вместо туннеля пользователь видит
 * понятное сообщение, а не сбой.
 */
object WgConfig {
    fun build(key: ServerKey, routing: Routing, apps: Set<String>, nonRu: List<String>): String =
        throw MtpException(WgController.notAvailable)
}

object WgController {
    const val notAvailable =
        "В этой сборке режим WireGuard выключен: выберите способ MTP/1–3 или загрузите .conf Amnezia"

    fun up(context: android.content.Context, configText: String) {
        throw MtpException(notAvailable)
    }
    fun down() {}
    fun isUp(): Boolean = false
    fun traffic(): Pair<Long, Long>? = null
}
