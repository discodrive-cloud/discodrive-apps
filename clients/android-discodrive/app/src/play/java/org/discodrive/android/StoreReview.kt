package org.discodrive.android

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import com.google.android.play.core.review.ReviewManagerFactory
import java.time.LocalDate

/**
 * Reviews in the Google Play build.
 *
 * The in-app review card comes up on its own, once [ReviewPolicy] says the app has been in
 * real use. Settings offers a "Rate" entry too, but that one opens the Play listing: Google
 * asks not to start the in-app flow from a button, because Play may have used up its quota
 * and then shows nothing, and the button would look broken.
 */
object StoreReview {
    const val AVAILABLE = true

    /** Called once per app start on the paired file browser. */
    fun onActiveSession(activity: Activity) {
        val ui = UiPrefs(activity)
        val today = LocalDate.now().toEpochDay()
        ui.recordActiveDay(today)
        if (!ReviewPolicy.shouldAsk(ui.reviewState, today)) return
        // Counted as asked before Play answers: whether a card was shown is not reported, and
        // a failure (no Play Store, offline) should not bring the question back tomorrow.
        ui.markReviewAsked(today)
        val manager = ReviewManagerFactory.create(activity)
        manager.requestReviewFlow().addOnCompleteListener { request ->
            if (request.isSuccessful && !activity.isFinishing && !activity.isDestroyed) {
                manager.launchReviewFlow(activity, request.result)
            }
        }
    }

    /** Opens the app's page in the Play Store, or in the browser if there is no store app. */
    fun openListing(context: Context) {
        val id = context.packageName
        try {
            // Straight to the Play Store app: other stores also handle market:// links, and
            // the system would ask which one to use.
            context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("market://details?id=$id"))
                .setPackage("com.android.vending")
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: ActivityNotFoundException) {
            try {
                context.startActivity(Intent(Intent.ACTION_VIEW,
                    Uri.parse("https://play.google.com/store/apps/details?id=$id"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (_: ActivityNotFoundException) { }
        }
    }
}
