package ru.usatyi.vpn

import android.content.Context
import android.net.VpnService

/** Какой трафик идёт через VPN. Выбирается отдельно для каждого ключа. */
enum class Routing(val label: String, val hint: String) {
    ALL("Весь трафик", "через VPN идут все сайты и приложения"),
    APPS("Только выбранные приложения", "через VPN идут только отмеченные приложения"),
    NOT_RU("Кроме российских IP", "российские адреса открываются напрямую, остальное через VPN"),
}

object Routes {
    /** IPv4-сети, которые идут через VPN в режиме «Кроме российских IP» (файл assets/routing/notru.txt). */
    fun notRu(context: Context): List<String> =
        context.assets.open("routing/notru.txt").bufferedReader().useLines { lines ->
            lines.map { it.trim() }.filter { it.isNotEmpty() && !it.startsWith("#") }.toList()
        }

    /**
     * Настраивает маршруты и приложения для VpnService.Builder в выбранном режиме.
     *
     * excludeSelf = false нужен цепочке Amnezia: там приложение уже исключено из VPN
     * (своё ядро нельзя заворачивать в собственный туннель), и второй вызов системы
     * привёл бы к ошибке «приложение одновременно и разрешено, и запрещено».
     */
    fun apply(
        b: VpnService.Builder,
        routing: Routing,
        apps: Set<String>,
        ownPackage: String,
        nonRu: List<String>,
        excludeSelf: Boolean = true,
    ) {
        when (routing) {
            Routing.ALL -> {
                b.addRoute("0.0.0.0", 0)
                b.addRoute("::", 0) // IPv6 направляем в туннель и отбрасываем: не утекает мимо VPN
                if (excludeSelf) b.addDisallowedApplication(ownPackage)
            }
            Routing.APPS -> {
                for (pkg in apps) {
                    try {
                        b.addAllowedApplication(pkg)
                    } catch (e: Exception) {
                        // приложение удалено после выбора: пропускаем
                    }
                }
                b.addRoute("0.0.0.0", 0)
                b.addRoute("::", 0)
            }
            Routing.NOT_RU -> {
                for (cidr in nonRu) {
                    val slash = cidr.indexOf('/')
                    b.addRoute(cidr.substring(0, slash), cidr.substring(slash + 1).toInt())
                }
                b.addRoute("::", 0)
                if (excludeSelf) b.addDisallowedApplication(ownPackage)
            }
        }
    }
}
