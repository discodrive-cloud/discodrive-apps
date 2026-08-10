package org.discodrive.fastsync

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.Environment
import androidx.core.app.NotificationCompat
import androidx.work.CoroutineWorker
import androidx.work.Data
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.concurrent.TimeUnit

/**
 * Runs one sync pass — periodically, and on demand when the user asks for one.
 *
 * A pass asked for by hand runs here rather than in the screen's coroutine because leaving the
 * app used to kill it: Android caches a process whose UI is gone, and a cached process loses
 * its network — DNS included, which surfaced as "lookup <host>: no such host" mid-sync. A
 * worker that promotes itself to the foreground keeps the process out of that state, so
 * switching apps no longer interrupts the transfer.
 */
class SyncWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {

    override suspend fun doWork(): Result {
        val prefs = Prefs(applicationContext)
        if (prefs.deviceToken == null) return Result.success()
        if (!Environment.isExternalStorageManager()) return Result.success()

        if (inputData.getBoolean(KEY_MANUAL, false)) {
            // Best-effort: a worker the system refuses to promote must still sync.
            runCatching { setForeground(foregroundInfo()) }
        }

        return withContext(Dispatchers.IO) {
            var error: String? = null
            ClientHolder.use(applicationContext) { client ->
                error = runCatching { client.syncOnce() }.exceptionOrNull()?.message
            }
            if (error == null) {
                Result.success()
            } else {
                // The pass is retried on its own schedule; the message is for the screen.
                Result.failure(workDataOf(KEY_ERROR to error))
            }
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
            .setContentTitle("DiscoDrive Fast Sync")
            .setContentText("Syncing…")
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
        private const val NAME = "fastsync-periodic"
        private const val CHANNEL = "sync"
        private const val NOTIFICATION_ID = 42
        private const val KEY_MANUAL = "manual"

        const val MANUAL_NAME = "fastsync-manual"
        const val KEY_ERROR = "error"

        fun schedule(ctx: Context) {
            val req = PeriodicWorkRequestBuilder<SyncWorker>(20, TimeUnit.MINUTES).build()
            WorkManager.getInstance(ctx)
                .enqueueUniquePeriodicWork(NAME, ExistingPeriodicWorkPolicy.KEEP, req)
        }

        /** Starts a pass the user asked for, or joins the one already running. */
        fun syncNow(ctx: Context) {
            val req = OneTimeWorkRequestBuilder<SyncWorker>()
                .setInputData(Data.Builder().putBoolean(KEY_MANUAL, true).build())
                .build()
            WorkManager.getInstance(ctx)
                .enqueueUniqueWork(MANUAL_NAME, ExistingWorkPolicy.KEEP, req)
        }

        fun cancel(ctx: Context) {
            WorkManager.getInstance(ctx).cancelUniqueWork(NAME)
            WorkManager.getInstance(ctx).cancelUniqueWork(MANUAL_NAME)
        }
    }
}
