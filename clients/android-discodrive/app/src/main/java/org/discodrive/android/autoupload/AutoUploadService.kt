package org.discodrive.android.autoupload

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import org.discodrive.android.Core
import org.discodrive.android.MainActivity
import org.discodrive.android.Prefs
import org.discodrive.android.R
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Runs an upload pass in the foreground so Android does not kill it when the user leaves the
 * app — a video over a phone connection outlives any background allowance.
 *
 * Builds its own Browser from the saved profile rather than borrowing the UI's: the service
 * outlives the activity, and sharing a handle across that boundary is how you end up using a
 * closed index.
 */
class AutoUploadService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val cancelled = AtomicBoolean(false)
    private var running = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_CANCEL) {
            cancelled.set(true)
            return START_NOT_STICKY
        }
        if (running) return START_NOT_STICKY
        running = true

        startForeground(NOTIFICATION_ID, notification(getString(R.string.autoupload_preparing), null))
        scope.launch {
            val prefs = Prefs(applicationContext)
            val journal = UploadJournal(applicationContext)
            try {
                val token = prefs.deviceToken
                if (token == null || !prefs.autoUpload) return@launch

                val blocked = Conditions.check(applicationContext, prefs)
                if (blocked != Block.NONE) return@launch

                val rootDir = File(android.os.Environment.getExternalStorageDirectory(), "DiscoDrive")
                val browser = Core.newBrowser(
                    prefs.serverURL, token, rootDir.path,
                    File(applicationContext.filesDir, "index.db").path, prefs.insecure,
                )
                try {
                    val runner = AutoUploadRunner(browser, journal, prefs)
                    runner.seedIfNeeded()
                    runner.runOnce(
                        progress = { done, total, name ->
                            notify(getString(R.string.autoupload_progress, done + 1, total), name)
                        },
                        isCancelled = { cancelled.get() },
                    )
                } finally {
                    runCatching { browser.close() }
                }
            } catch (e: Exception) {
                // A failed pass is not worth a crash: the files stay in the journal as
                // deferred and the next trigger picks them up.
            } finally {
                journal.close()
                stopSelf()
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun notify(title: String, text: String?) {
        val nm = getSystemService(NotificationManager::class.java)
        nm?.notify(NOTIFICATION_ID, notification(title, text))
    }

    private fun notification(title: String, text: String?): Notification {
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && nm?.getNotificationChannel(CHANNEL) == null) {
            nm?.createNotificationChannel(
                NotificationChannel(CHANNEL, getString(R.string.autoupload_channel), NotificationManager.IMPORTANCE_LOW)
            )
        }
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val cancel = PendingIntent.getService(
            this, 1, Intent(this, AutoUploadService::class.java).setAction(ACTION_CANCEL),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        return Notification.Builder(this, CHANNEL)
            .setContentTitle(title)
            .setContentText(text)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentIntent(open)
            .setOngoing(true)
            .addAction(
                Notification.Action.Builder(null, getString(R.string.cancel), cancel).build()
            )
            .build()
    }

    companion object {
        private const val CHANNEL = "autoupload"
        private const val NOTIFICATION_ID = 4201
        const val ACTION_CANCEL = "org.discodrive.android.AUTOUPLOAD_CANCEL"

        fun start(context: Context) {
            context.startForegroundService(Intent(context, AutoUploadService::class.java))
        }
    }
}
