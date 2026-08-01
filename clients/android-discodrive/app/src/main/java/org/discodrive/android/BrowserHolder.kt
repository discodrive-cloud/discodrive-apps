package org.discodrive.android

import android.content.Context
import android.os.Environment
import mobile.Browser
import java.io.File

/**
 * The process-wide [Browser].
 *
 * The index is a SQLite file, and a second handle on it deadlocks against the first: the UI
 * and the auto-upload service both wanted one, and opening both made the app fail with
 * "database is locked (SQLITE_BUSY)" — reproduced on an emulator. Both now share this one.
 *
 * Callers must not close what they get; [close] belongs to unpairing.
 */
object BrowserHolder {

    private var browser: Browser? = null

    /** Opens the browser from the saved profile, or returns null when not paired yet. */
    @Synchronized
    fun get(context: Context): Browser? {
        browser?.let { return it }
        val prefs = Prefs(context)
        val token = prefs.deviceToken ?: return null
        if (prefs.serverURL.isEmpty()) return null
        val rootDir = File(Environment.getExternalStorageDirectory(), "DiscoDrive")
        val b = Core.newBrowser(
            prefs.serverURL, token, rootDir.path,
            File(context.filesDir, "index.db").path, prefs.insecure,
        )
        browser = b
        return b
    }

    /** True when a browser is already open — used to decide whether a refresh is needed. */
    @Synchronized
    fun isOpen(): Boolean = browser != null

    @Synchronized
    fun close() {
        runCatching { browser?.close() }
        browser = null
    }
}
