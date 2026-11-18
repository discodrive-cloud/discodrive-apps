package org.discodrive.android

import android.app.Activity
import android.content.Context

/**
 * The GitHub build is not installed from Google Play, so there is no listing to rate it on
 * and no in-app review: nothing is asked and Settings shows no "Rate" entry. The Play build
 * has the real one (src/play).
 */
object StoreReview {
    const val AVAILABLE = false
    fun onActiveSession(activity: Activity) {}
    fun openListing(context: Context) {}
}
