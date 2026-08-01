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
 * periodic run. It only lives as long as the app's process — [AutoUploadWorker] covers the
 * rest — and it deliberately does not inspect the event: the scanner already decides what is
 * worth uploading, and a `CLOSE_WRITE` on a half-saved file would only race it.
 */
class FolderObservers(private val context: Context) {

    private val observers = mutableListOf<FileObserver>()
    private val handler = Handler(Looper.getMainLooper())
    private var pending: Runnable? = null

    /**
     * A camera writes a photo, then its thumbnail, then touches the folder — several events
     * for one picture. Waiting a moment turns that burst into a single pass.
     */
    private val debounceMs = 5_000L

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
            handler.postDelayed(r, debounceMs)
        }
    }
}
