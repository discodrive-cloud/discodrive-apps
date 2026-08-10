package org.discodrive.android.autoupload

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import org.discodrive.android.BrowserHolder
import org.discodrive.android.MainActivity
import org.discodrive.android.Prefs
import org.discodrive.android.R
import java.util.concurrent.TimeUnit

/**
 * Runs an upload pass, as a foreground worker so a long transfer survives the app going away.
 *
 * The pass lives here rather than in a Service because a Service cannot be started from the
 * background on Android 12+: an earlier version called startForegroundService() from this
 * worker and only worked while the app happened to be on screen — exactly the case that does
 * NOT matter. WorkManager's own foreground support has no such restriction.
 *
 * Triggered two ways: [runNow] right after a folder changes, and a periodic run that catches
 * whatever the observer missed while the process was dead. WorkManager's floor is 15 minutes
 * and the OS defers freely (Doze, vendor limits), so this is "eventually", not "on time" —
 * which is why the UI never promises instant upload.
 */
class AutoUploadWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {

    override suspend fun doWork(): Result {
        val prefs = Prefs(applicationContext)
        if (!prefs.autoUpload || prefs.deviceToken == null) return Result.success()
        if (prefs.rules.none { it.enabled }) return Result.success()
        if (Conditions.check(applicationContext, prefs) != Block.NONE) {
            // Not a failure: the conditions will be met later and the next run picks it up.
            return Result.success()
        }

        setForegroundSafely(applicationContext.getString(R.string.autoupload_preparing), null)

        val journal = UploadJournal(applicationContext)
        return try {
            // Borrowed for the whole pass: re-pairing closes the shared index, and doing that
            // under a running batch surfaced as "sql: database is closed". The close now waits
            // for this to finish instead.
            BrowserHolder.use(applicationContext) { browser ->
                val runner = AutoUploadRunner(browser, journal, prefs)
                runner.seedIfNeeded()
                runner.runOnce(
                    progress = { done, total, name ->
                        setForegroundSafely(
                            applicationContext.getString(R.string.autoupload_progress, done + 1, total), name,
                        )
                    },
                    isCancelled = { isStopped },
                )
            }
            Result.success()
        } catch (e: Exception) {
            // The files stay in the journal as deferred; retrying the whole pass on a
            // schedule beats hammering it now.
            Result.success()
        } finally {
            journal.close()
        }
    }

    /**
     * Progress updates are best-effort: a worker that has already been stopped, or one the
     * system refuses to promote, must not turn a working upload into a crash.
     */
    private fun setForegroundSafely(title: String, text: String?) {
        try {
            @Suppress("DEPRECATION")
            val info = ForegroundInfo(
                NOTIFICATION_ID, notification(applicationContext, title, text),
                ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
            )
            kotlinx.coroutines.runBlocking { setForeground(info) }
        } catch (e: Exception) {
            // ignored on purpose
        }
    }

    companion object {
        private const val PERIODIC = "autoupload-periodic"
        private const val ONE_SHOT = "autoupload-now"
        private const val CHANNEL = "autoupload"
        private const val NOTIFICATION_ID = 4201

        fun schedule(ctx: Context, wifiOnly: Boolean) {
            val req = PeriodicWorkRequestBuilder<AutoUploadWorker>(20, TimeUnit.MINUTES)
                .setConstraints(constraints(wifiOnly))
                .build()
            // UPDATE, not KEEP: the constraint follows the Wi-Fi setting, and a stale
            // schedule would keep the old one forever.
            WorkManager.getInstance(ctx)
                .enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.UPDATE, req)
        }

        /** A pass as soon as the system allows — used when a watched folder changes. */
        fun runNow(ctx: Context, wifiOnly: Boolean) {
            val req = OneTimeWorkRequestBuilder<AutoUploadWorker>()
                .setConstraints(constraints(wifiOnly))
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .build()
            // KEEP: several photos landing at once must not queue several passes; the one
            // already running picks them all up.
            WorkManager.getInstance(ctx).enqueueUniqueWork(ONE_SHOT, ExistingWorkPolicy.KEEP, req)
        }

        fun cancel(ctx: Context) {
            WorkManager.getInstance(ctx).cancelUniqueWork(PERIODIC)
            WorkManager.getInstance(ctx).cancelUniqueWork(ONE_SHOT)
        }

        private fun constraints(wifiOnly: Boolean) = Constraints.Builder()
            .setRequiredNetworkType(if (wifiOnly) NetworkType.UNMETERED else NetworkType.CONNECTED)
            .build()

        fun notification(ctx: Context, title: String, text: String?): Notification {
            val nm = ctx.getSystemService(NotificationManager::class.java)
            if (nm?.getNotificationChannel(CHANNEL) == null) {
                nm?.createNotificationChannel(
                    NotificationChannel(
                        CHANNEL, ctx.getString(R.string.autoupload_channel),
                        NotificationManager.IMPORTANCE_LOW,
                    )
                )
            }
            val open = PendingIntent.getActivity(
                ctx, 0, Intent(ctx, MainActivity::class.java),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            return Notification.Builder(ctx, CHANNEL)
                .setContentTitle(title)
                .setContentText(text)
                .setSmallIcon(android.R.drawable.stat_sys_upload)
                .setContentIntent(open)
                .setOngoing(true)
                .build()
        }
    }
}
