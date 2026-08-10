package org.discodrive.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Pulls the change feed into the local index, in the foreground.
 *
 * The pull used to run in the screen's coroutine, which Android kills off the moment the app
 * stops being visible: a cached process loses its network, DNS first, so switching apps
 * mid-pull ended it with "lookup <host>: no such host". Promoting the work to a foreground
 * service keeps the process out of that state — the first pull after pairing is long enough
 * that the user will certainly look elsewhere while it runs.
 */
class RefreshWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {

    override suspend fun doWork(): Result = coroutineScope {
        if (Prefs(applicationContext).deviceToken == null) return@coroutineScope Result.success()

        // Every launch pulls, and most pulls are a fraction of a second — promoting instantly
        // would flash a notification each time the app opens. Wait a moment first: only a pull
        // long enough for the user to wander off needs protecting from being cached, and it is
        // a long way short of Android's limit for getting this in place.
        // Best-effort throughout: a worker the system refuses to promote must still do the work.
        val promote = launch {
            delay(3_000)
            runCatching { setForeground(foregroundInfo()) }
        }
        try {
            withContext(Dispatchers.IO) {
                var error: String? = null
                BrowserHolder.use(applicationContext) { browser ->
                    error = runCatching { browser.refresh() }.exceptionOrNull()?.message
                }
                if (error == null) Result.success() else Result.failure(workDataOf(KEY_ERROR to error))
            }
        } finally {
            promote.cancel()
        }
    }

    private fun foregroundInfo(): ForegroundInfo {
        val nm = applicationContext.getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, "Sync", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val n = NotificationCompat.Builder(applicationContext, CHANNEL)
            .setContentTitle(applicationContext.getString(R.string.app_name))
            .setContentText(applicationContext.getString(R.string.sync_running))
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .build()
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            ForegroundInfo(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            ForegroundInfo(NOTIFICATION_ID, n)
        }
    }

    companion object {
        private const val CHANNEL = "sync"
        private const val NOTIFICATION_ID = 43

        const val NAME = "discodrive-refresh"
        const val KEY_ERROR = "error"

        /** Starts a pull, or joins the one already running. */
        fun start(ctx: Context) {
            WorkManager.getInstance(ctx).enqueueUniqueWork(
                NAME, ExistingWorkPolicy.KEEP, OneTimeWorkRequestBuilder<RefreshWorker>().build(),
            )
        }

        fun cancel(ctx: Context) = WorkManager.getInstance(ctx).cancelUniqueWork(NAME)
    }
}
