package org.discodrive.android.sync

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
import org.discodrive.android.Prefs
import org.discodrive.android.R
import java.util.concurrent.TimeUnit

/**
 * One folder-sync pass — periodically, and on demand (the button, or an event from the server).
 *
 * A pass runs here rather than in the screen's coroutine because leaving the app used to
 * kill it: Android caches a process whose UI is gone, and a cached process loses its
 * network, DNS first. A worker promoted to the foreground keeps the process out of that
 * state, so switching apps no longer interrupts the transfer.
 */
class SyncWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {

    override suspend fun doWork(): Result {
        val prefs = Prefs(applicationContext)
        if (!prefs.folderSync || prefs.deviceToken == null) return Result.success()
        if (!Environment.isExternalStorageManager()) return Result.success()

        if (inputData.getBoolean(KEY_MANUAL, false)) {
            // Best-effort: a worker the system refuses to promote must still sync.
            runCatching { setForeground(foregroundInfo()) }
        }

        return withContext(Dispatchers.IO) {
            var error: String? = null
            SyncHolder.use(applicationContext) { client ->
                error = runCatching { client.syncOnce() }.exceptionOrNull()?.message
            }
            if (error == null) Result.success()
            else Result.failure(workDataOf(KEY_ERROR to error)) // retried on its own schedule; the text is for the screen
        }
    }

    private fun foregroundInfo(): ForegroundInfo {
        val nm = applicationContext.getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, applicationContext.getString(R.string.sync_channel), NotificationManager.IMPORTANCE_LOW)
            )
        }
        val n = NotificationCompat.Builder(applicationContext, CHANNEL)
            .setContentTitle(applicationContext.getString(R.string.app_name))
            .setContentText(applicationContext.getString(R.string.sync_notification))
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
        private const val NAME = "folder-sync-periodic"
        private const val CHANNEL = "folder-sync"
        private const val NOTIFICATION_ID = 43
        private const val KEY_MANUAL = "manual"

        const val MANUAL_NAME = "folder-sync-manual"
        const val KEY_ERROR = "error"

        /** Marker text of a pass stopped by the mass-deletion check (gomobile keeps only the message). */
        const val BULK_DELETE_MARKER = "refusing to delete"

        fun schedule(ctx: Context) {
            val req = PeriodicWorkRequestBuilder<SyncWorker>(20, TimeUnit.MINUTES).build()
            WorkManager.getInstance(ctx).enqueueUniquePeriodicWork(NAME, ExistingPeriodicWorkPolicy.KEEP, req)
        }

        /** Starts a pass now, or joins the one already running. */
        fun syncNow(ctx: Context) {
            val req = OneTimeWorkRequestBuilder<SyncWorker>()
                .setInputData(Data.Builder().putBoolean(KEY_MANUAL, true).build())
                .build()
            WorkManager.getInstance(ctx).enqueueUniqueWork(MANUAL_NAME, ExistingWorkPolicy.KEEP, req)
        }

        fun cancel(ctx: Context) {
            WorkManager.getInstance(ctx).cancelUniqueWork(NAME)
            WorkManager.getInstance(ctx).cancelUniqueWork(MANUAL_NAME)
        }
    }
}
