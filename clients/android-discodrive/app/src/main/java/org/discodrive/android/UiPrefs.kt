package org.discodrive.android

import android.content.Context
import android.content.SharedPreferences
import androidx.appcompat.app.AppCompatDelegate

/** The app's colour scheme: the phone's setting, or always light, or always dark. */
enum class ThemeMode(val nightMode: Int) {
    SYSTEM(AppCompatDelegate.MODE_NIGHT_FOLLOW_SYSTEM),
    LIGHT(AppCompatDelegate.MODE_NIGHT_NO),
    DARK(AppCompatDelegate.MODE_NIGHT_YES);

    /**
     * Switches every screen over. AppCompat changes the activities' configuration, so the
     * window background (DayNight) and Compose's isSystemInDarkTheme() both follow, and
     * open activities are recreated.
     */
    fun apply() = AppCompatDelegate.setDefaultNightMode(nightMode)
}

/** How long the app has been in use, for deciding when to ask for a review. Days are epoch days. */
data class ReviewState(
    val firstDay: Long? = null,
    val lastDay: Long? = null,
    val activeDays: Int = 0,
    val askedDay: Long? = null,
)

/**
 * Asks for a review only from someone who has actually been using the app: on several
 * different days, spread over at least a week. After asking, it waits months before asking
 * again. Google Play has its own quota on top and may show nothing at all.
 */
object ReviewPolicy {
    const val MIN_ACTIVE_DAYS = 4
    const val MIN_DAYS_SINCE_FIRST = 7L
    const val DAYS_BETWEEN_ASKS = 120L

    fun shouldAsk(s: ReviewState, today: Long): Boolean {
        val first = s.firstDay ?: return false
        if (s.activeDays < MIN_ACTIVE_DAYS) return false
        if (today - first < MIN_DAYS_SINCE_FIRST) return false
        val asked = s.askedDay ?: return true
        return today - asked >= DAYS_BETWEEN_ASKS
    }
}

/**
 * Interface preferences: not secret, and kept apart from [Prefs] on purpose. That store is
 * encrypted and wiped on unpair; the theme and the review counters belong to the phone's
 * user, not to a pairing, and are read in Application.onCreate before any screen.
 */
class UiPrefs internal constructor(private val sp: SharedPreferences) {
    constructor(context: Context) : this(context.getSharedPreferences("ui", Context.MODE_PRIVATE))

    var themeMode: ThemeMode
        get() = sp.getString("themeMode", null)
            ?.let { name -> ThemeMode.entries.firstOrNull { it.name == name } } ?: ThemeMode.SYSTEM
        set(v) { sp.edit().putString("themeMode", v.name).apply() }

    val reviewState: ReviewState
        get() = ReviewState(
            firstDay = sp.getLong("reviewFirstDay", -1).takeIf { it >= 0 },
            lastDay = sp.getLong("reviewLastDay", -1).takeIf { it >= 0 },
            activeDays = sp.getInt("reviewActiveDays", 0),
            askedDay = sp.getLong("reviewAskedDay", -1).takeIf { it >= 0 },
        )

    /** Counts [day] as a day the app was used; the same day twice counts once. */
    fun recordActiveDay(day: Long) {
        val s = reviewState
        if (s.lastDay == day) return
        sp.edit()
            .putLong("reviewFirstDay", s.firstDay ?: day)
            .putLong("reviewLastDay", day)
            .putInt("reviewActiveDays", s.activeDays + 1)
            .apply()
    }

    fun markReviewAsked(day: Long) { sp.edit().putLong("reviewAskedDay", day).apply() }
}
