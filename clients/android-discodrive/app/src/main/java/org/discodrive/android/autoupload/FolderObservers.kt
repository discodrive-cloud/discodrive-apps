package org.discodrive.android.autoupload

import android.content.Context
import android.os.FileObserver
import android.os.Handler
import android.os.Looper
import org.discodrive.android.Prefs
import java.io.File

/**
 * Watches every rule's folder and starts a pass shortly after something lands there.
 *
 * This is what makes a photo appear on the server in seconds rather than at the next
 * periodic run. One instance per process, started when the app starts ([DiscoDriveApp]);
 * it lives as long as the process — [AutoUploadWorker] covers the rest — and it
 * deliberately does not inspect the event: the scanner already decides what is
 * worth uploading, and a `CLOSE_WRITE` on a half-saved file would only race it.
 */
class FolderObservers private constructor(private val context: Context) {

    companion object {
        @Volatile private var instance: FolderObservers? = null

        /**
         * A camera writes a photo, then its thumbnail, then touches the folder — several
         * events for one picture; waiting turns that burst into a single pass. It must also
         * outlast [SourceScanner.SETTLE_MS]: a pass any sooner finds the new photo still
         * "being written", skips it, and nothing starts another until the periodic run up
         * to 20 minutes later.
         */
        const val DEBOUNCE_MS = SourceScanner.SETTLE_MS + 2_000L

        fun get(context: Context): FolderObservers = instance ?: synchronized(this) {
            instance ?: FolderObservers(context.applicationContext).also { instance = it }
        }
    }

    private val observers = mutableListOf<FileObserver>()
    private val handler = Handler(Looper.getMainLooper())
    private var pending: Runnable? = null

    @Synchronized
    fun start() {
        stop()
        val prefs = Prefs(context)
        if (!prefs.autoUpload) return
        for (rule in prefs.rules.filter { it.enabled }) {
            val dir = File(rule.sourcePath)
            if (!dir.isDirectory) continue
            val o = object : FileObserver(dir, CREATE or CLOSE_WRITE or MOVED_TO) {
                override fun onEvent(event: Int, path: String?) {
                    if (path == null) return
                    schedulePass()
                }
            }
            runCatching { o.startWatching() }.onSuccess { observers.add(o) }
        }
    }

    @Synchronized
    fun stop() {
        observers.forEach { runCatching { it.stopWatching() } }
        observers.clear()
        pending?.let { handler.removeCallbacks(it) }
        pending = null
    }

    private fun schedulePass() {
        handler.post {
            pending?.let { handler.removeCallbacks(it) }
            val r = Runnable {
                pending = null
                val prefs = Prefs(context)
                if (prefs.autoUpload) AutoUploadWorker.runNow(context, prefs.wifiOnly)
            }
            pending = r
            handler.postDelayed(r, DEBOUNCE_MS)
        }
    }
}
