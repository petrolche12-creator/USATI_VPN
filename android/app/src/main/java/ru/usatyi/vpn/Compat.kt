package ru.usatyi.vpn

import android.app.Activity
import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.text.Editable
import android.text.TextWatcher
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import android.widget.TextView

/**
 * Системные замены библиотек AndroidX/Material.
 *
 * Приложение собрано только на Android SDK: так его можно собрать где угодно, без Gradle и
 * без скачивания библиотек. Здесь — те немногие вещи из androidx, которыми пользовались.
 */

/** Тема приложения: цвета Material 3 вручную (светлая/тёмная — системой). */
object Theme {
    val primary = 0xFF3F51B5.toInt()
    val primaryContainer = 0xFFE0E3FF.toInt()
    val onPrimaryContainer = 0xFF11184A.toInt()
    val secondaryContainer = 0xFFFFE08C.toInt()
    val onSecondaryContainer = 0xFF4A3B00.toInt()
    val errorContainer = 0xFFF9DEDC.toInt()
    val onErrorContainer = 0xFF410E0B.toInt()
    val surface = 0xFFFBF8FF.toInt()
    val surfaceVariant = 0xFFE3E1EC.toInt()
    val onSurface = 0xFF1B1B1F.toInt()
    val onSurfaceVariant = 0xFF46464F.toInt()
    val outline = 0xFF777680.toInt()
}

/** Цвета тона карточки — то, что раньше давал MaterialColors. */
object Colors {
    fun container(tone: Tone): Int = when (tone) {
        Tone.NEUTRAL -> Theme.surfaceVariant
        Tone.WORKING -> Theme.secondaryContainer
        Tone.ACTIVE -> Theme.primaryContainer
        Tone.ERROR -> Theme.errorContainer
    }

    fun onContainer(tone: Tone): Int = when (tone) {
        Tone.NEUTRAL -> Theme.onSurfaceVariant
        Tone.WORKING -> Theme.onSecondaryContainer
        Tone.ACTIVE -> Theme.onPrimaryContainer
        Tone.ERROR -> Theme.onErrorContainer
    }
}

/** Скруглённый фон — вместо MaterialCardView. */
fun rounded(color: Int, radiusDp: Float, strokeDp: Float = 0f, strokeColor: Int = 0): GradientDrawable =
    GradientDrawable().apply {
        setColor(color)
        cornerRadius = radiusDp * 3f // ~ плотность экрана; для карточек это незаметно
        if (strokeDp > 0f) setStroke((strokeDp * 3f).toInt().coerceAtLeast(1), strokeColor)
    }

/** Карточка: FrameLayout с фоном (замена com.google.android.material.card.MaterialCardView). */
class CardView(context: Context) : FrameLayout(context) {
    private var radius = 20f
    private var bg = Theme.surfaceVariant
    private var strokeW = 0f
    private var strokeC = Theme.outline

    init {
        setPadding(0, 0, 0, 0)
        applyBg()
    }

    fun setCardBackgroundColor(color: Int) {
        bg = color
        applyBg()
    }

    fun setStrokeWidth(w: Int) {
        strokeW = w.toFloat() / 3f
        applyBg()
    }

    fun setStrokeColor(color: Int) {
        strokeC = color
        applyBg()
    }

    fun setRadiusDp(r: Float) {
        radius = r
        applyBg()
    }

    private fun applyBg() {
        background = rounded(bg, radius, strokeW, strokeC)
    }
}

/** Индикатор расхода трафика (замена LinearProgressIndicator). */
class ProgressLine(context: Context) : View(context) {
    var progress: Int = 0
        set(value) {
            field = value.coerceIn(0, 100)
            invalidate()
        }

    override fun onDraw(canvas: android.graphics.Canvas) {
        val h = height.toFloat()
        val w = width.toFloat()
        canvas.drawRoundRect(0f, 0f, w, h, h / 2, h / 2, solid(Theme.surfaceVariant))
        if (progress > 0) {
            canvas.drawRoundRect(0f, 0f, w * progress / 100f, h, h / 2, h / 2, solid(Theme.primary))
        }
    }

    private fun solid(c: Int) = android.graphics.Paint().apply {
        color = c
        isAntiAlias = true
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        setMeasuredDimension(MeasureSpec.getSize(widthMeasureSpec), (6 * resources.displayMetrics.density).toInt())
    }
}

/** Нижняя навигация из системных кнопок (замена BottomNavigationView). */
class BottomBar(context: Context) : LinearLayout(context) {
    private val titles = mutableListOf<String>()
    private val buttons = mutableListOf<TextView>()
    var onPick: ((Int) -> Unit)? = null

    init {
        orientation = HORIZONTAL
        setBackgroundColor(Theme.surface)
    }

    fun setTitles(list: List<String>) {
        removeAllViews()
        titles.clear()
        titles.addAll(list)
        buttons.clear()
        for ((i, t) in titles.withIndex()) {
            val b = TextView(context).apply {
                text = t
                gravity = Gravity.CENTER
                setPadding(0, dp(14), 0, dp(14))
                textSize = 13f
                layoutParams = LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
                setOnClickListener { select(i) }
            }
            buttons.add(b)
            addView(b)
        }
        select(0)
    }

    fun select(index: Int) {
        for ((i, b) in buttons.withIndex()) {
            b.setTextColor(if (i == index) Theme.primary else Theme.onSurfaceVariant)
            b.setTypeface(null, if (i == index) android.graphics.Typeface.BOLD else android.graphics.Typeface.NORMAL)
        }
        onPick?.invoke(index)
    }

    private fun dp(v: Int): Int = (v * resources.displayMetrics.density).toInt()
}

/**
 * Диалог на системном android.app.AlertDialog (замена androidx AlertDialog).
 * Нужен, чтобы код экранов не менялся: те же методы setTitle/setView/setPositiveButton.
 */
class DialogBuilder(private val ctx: Context) {
    private val b = android.app.AlertDialog.Builder(ctx)

    fun setTitle(t: CharSequence?): DialogBuilder = apply { b.setTitle(t) }
    fun setMessage(m: CharSequence?): DialogBuilder = apply { b.setMessage(m) }
    fun setView(v: View?): DialogBuilder = apply { b.setView(v) }
    fun setCancelable(c: Boolean): DialogBuilder = apply { b.setCancelable(c) }
    fun setPositiveButton(t: CharSequence?, l: ((android.content.DialogInterface, Int) -> Unit)?): DialogBuilder =
        apply { b.setPositiveButton(t) { d, w -> l?.invoke(d, w) } }

    fun setNegativeButton(t: CharSequence?, l: ((android.content.DialogInterface, Int) -> Unit)?): DialogBuilder =
        apply { b.setNegativeButton(t) { d, w -> l?.invoke(d, w) } }

    fun setNeutralButton(t: CharSequence?, l: ((android.content.DialogInterface, Int) -> Unit)?): DialogBuilder =
        apply { b.setNeutralButton(t) { d, w -> l?.invoke(d, w) } }

    fun setItems(items: Array<String>?, l: ((android.content.DialogInterface, Int) -> Unit)?): DialogBuilder =
        apply { b.setItems(items) { d, w -> l?.invoke(d, w) } }

    fun setMultiChoiceItems(
        items: Array<String>?,
        checked: BooleanArray?,
        l: ((android.content.DialogInterface, Int, Boolean) -> Unit)?,
    ): DialogBuilder = apply { b.setMultiChoiceItems(items, checked) { d, w, c -> l?.invoke(d, w, c) } }

    fun create(): android.app.AlertDialog = b.create()
    fun show(): android.app.AlertDialog = b.show()
}

/** Уведомление в системном стиле (замена NotificationCompat). */
object Notifications {
    const val PRIORITY_LOW = Notification.PRIORITY_LOW

    class Builder(ctx: Context, channel: String) {
        private val b = Notification.Builder(ctx).setChannelId(channel)
        fun setSmallIcon(id: Int): Builder = apply { b.setSmallIcon(id) }
        fun setContentTitle(t: CharSequence?): Builder = apply { b.setContentTitle(t) }
        fun setContentText(t: CharSequence?): Builder = apply { b.setContentText(t) }
        fun setContentIntent(i: PendingIntent?): Builder = apply { b.setContentIntent(i) }
        fun setOngoing(o: Boolean): Builder = apply { b.setOngoing(o) }
        fun setPriority(p: Int): Builder = apply { b.setPriority(p) }
        fun addAction(icon: Int, t: CharSequence?, i: PendingIntent?): Builder = apply {
            b.addAction(Notification.Action.Builder(icon, t, i).build())
        }
        fun build(): Notification = b.build()
    }
}

/** Ввод текста: аналог androidx doAfterTextChanged. */
fun EditText.afterTextChanged(action: (String) -> Unit) {
    addTextChangedListener(object : TextWatcher {
        override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
        override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
        override fun afterTextChanged(s: Editable?) {
            action(s?.toString().orEmpty())
        }
    })
}

/** Запуск службы переднего плана (замена ContextCompat.startForegroundService). */
fun startForegroundService(ctx: Context, i: android.content.Intent) {
    if (android.os.Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(i) else ctx.startService(i)
}

/** Кнопка-«плоская»: прозрачный фон, цвет текста. */
fun flatButton(ctx: Context, text: String, color: Int = Theme.primary): Button = Button(ctx).apply {
    this.text = text
    isAllCaps = false
    setTextColor(color)
    background = rounded(Color.TRANSPARENT, 0f)
    setPadding(0, 0, 0, 0)
}

/** Горизонтальный контейнер (используется в диалогах). */
fun row(ctx: Context, vararg views: View): LinearLayout = LinearLayout(ctx).apply {
    orientation = LinearLayout.HORIZONTAL
    for (v in views) addView(v)
}

/** Цвет тона для иконок/текста. */
fun tint(c: Int): ColorStateList = ColorStateList.valueOf(c)

/** Прокрутка текста лога. */
class ScrollText(ctx: Context) : HorizontalScrollView(ctx)

/**
 * Выбор файла через системное окно документов (замена ActivityResultContracts.OpenDocument).
 * Открывает «Документы», результат приходит в onActivityResult.
 */
abstract class FilePicker {
    protected var activity: Activity? = null
        private set
    private var requestCode = 0

    fun attach(a: Activity) {
        activity = a
        requestCode = 9000 + (System.identityHashCode(this) % 900)
    }

    fun launch() {
        val a = activity ?: return
        val i = android.content.Intent(android.content.Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(android.content.Intent.CATEGORY_OPENABLE)
            type = "*/*"
        }
        @Suppress("DEPRECATION")
        a.startActivityForResult(i, requestCode)
    }

    fun deliver(request: Int, resultCode: Int, data: android.content.Intent?) {
        if (request != requestCode) return
        onPicked(if (resultCode == Activity.RESULT_OK) data?.data else null)
    }

    abstract fun onPicked(uri: android.net.Uri?)
}

/** Простой приёмник результата (разрешения, системные окна). */
abstract class SimpleLauncher {
    protected var activity: Activity? = null
        private set
    private var requestCode = 0

    fun attach(a: Activity) {
        activity = a
        requestCode = 9500 + (System.identityHashCode(this) % 400)
    }

    fun launch(permission: String) {
        val a = activity ?: return
        a.requestPermissions(arrayOf(permission), requestCode)
    }

    fun deliver(request: Int) {
        if (request == requestCode) onResult()
    }

    abstract fun onResult()
}

/** Окно разрешения VPN: системный Intent, результат — в onLaunchResult. */
class PermissionLauncher(private val onResult: (Activity, Int) -> Unit) {
    private var activity: Activity? = null
    private var requestCode = 0

    fun attach(a: Activity) {
        activity = a
        requestCode = 9800 + (System.identityHashCode(this) % 100)
    }

    fun launch(i: android.content.Intent) {
        val a = activity ?: return
        @Suppress("DEPRECATION")
        a.startActivityForResult(i, requestCode)
    }

    fun deliver(request: Int, resultCode: Int) {
        if (request != requestCode) return
        activity?.let { onResult(it, resultCode) }
    }
}
