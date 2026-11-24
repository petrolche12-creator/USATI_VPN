package ru.usatyi.vpn

import android.content.Context
import android.net.Uri
import android.view.View
import androidx.appcompat.app.AlertDialog
import ru.usatyi.vpn.databinding.DialogTextBinding

/**
 * Загрузка файла .conf Amnezia и показ хода цепочки.
 *
 * Пользователь скачивает .conf в Amnezia («Поделиться» → файл), затем в нашем приложении
 * нажимает «Добавить Amnezia» и выбирает этот файл в системном выборе документов.
 */
object ChainUi {

    /** Читает выбранный файл и сохраняет его после строгой проверки. */
    fun loadConf(ctx: Context, uri: Uri, onDone: (String) -> Unit) {
        Thread {
            val result = runCatching {
                val text = ctx.contentResolver.openInputStream(uri)?.use { stream ->
                    // .conf — текстовый файл; больше 64 КБ в них не бывает
                    val bytes = stream.readBytes()
                    if (bytes.size > 64 * 1024) throw MtpException("файл слишком большой для .conf")
                    String(bytes, Charsets.UTF_8)
                } ?: throw MtpException("не удалось открыть файл")

                val name = displayName(ctx, uri)
                val cfg = AmneziaStore.save(ctx, text, name)
                "Загружен «${cfg.name}»: ${cfg.peer.endpoint}, ${cfg.kind}" +
                    if (cfg.obf.isEmpty()) "" else " (обфускация AmneziaWG)"
            }
            onDone(result.getOrElse { e -> "Не получилось: ${e.message ?: e.javaClass.simpleName}" })
        }.start()
    }

    /** Имя файла из системного Uri (для экрана). */
    private fun displayName(ctx: Context, uri: Uri): String {
        val fromProvider = runCatching {
            ctx.contentResolver.query(uri, arrayOf(android.provider.OpenableColumns.DISPLAY_NAME), null, null, null)
                ?.use { c -> if (c.moveToFirst()) c.getString(0) else null }
        }.getOrNull()
        val raw = fromProvider ?: uri.lastPathSegment ?: "amnezia.conf"
        return raw.substringAfterLast('/').removeSuffix(".conf").ifEmpty { "amnezia" }
    }

    /** Окно «что загружено» с подсказкой, где взять файл. */
    fun showHowTo(ctx: MainActivity) {
        val d = DialogTextBinding.inflate(ctx.layoutInflater)
        d.dtText.text = """
            Файл .conf — это настройки вашего сервера Amnezia.

            1. Откройте приложение Amnezia на этом телефоне.
            2. Нажмите на свой сервер → «Поделиться» (или «Экспорт»).
            3. Выберите «Файл .conf» и сохраните его в «Загрузки».
            4. Вернитесь сюда и нажмите «Добавить Amnezia» — выберите этот файл.

            Приложение сначала подключается к серверу Amnezia по этому файлу,
            а уже через него — к вашему VPN по SSH. Без файла цепочка не работает.
        """.trimIndent()
        AlertDialog.Builder(ctx)
            .setTitle("Как получить .conf")
            .setView(d.root)
            .setPositiveButton("Выбрать файл") { _, _ -> ctx.pickAmneziaFile() }
            .setNegativeButton("Понятно", null)
            .show()
    }
}
