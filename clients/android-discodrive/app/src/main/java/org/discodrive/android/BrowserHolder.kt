package org.discodrive.android

import android.content.Context
import android.os.Environment
import mobile.Browser
import java.io.File
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * The process-wide [Browser].
 *
 * The index is a SQLite file, and a second handle on it deadlocks against the first: the UI
 * and the auto-upload service both wanted one, and opening both made the app fail with
 * "database is locked (SQLITE_BUSY)" — reproduced on an emulator. Both now share this one.
 *
 * Callers must not close what they get; [close] belongs to re-pairing and unpairing. Borrow it
 * through [use], which keeps it open for the duration: closing it under a caller that was
 * mid-operation surfaced as "sql: database is closed" — seen when re-pairing while an
 * auto-upload pass, or the refresh that follows pairing, was still running.
 */
object BrowserHolder {

    private val lock = ReentrantLock()
    private val idle = lock.newCondition()
    private val operations = ReentrantLock()

    private var browser: Browser? = null
    private var inUse = 0
    private var closing = false

    /**
     * Opens the browser from the saved profile, or returns null when not paired yet.
     *
     * Opening performs no request, so a browser built on a token the server has since replaced
     * looks healthy until something asks — which is why re-pairing calls [close].
     */
    fun get(context: Context): Browser? = lock.withLock { open(context) }

    private fun open(context: Context): Browser? {
        if (closing) return null
        val prefs = Prefs(context)
        if (prefs.unpairing) return null
        browser?.let { return it }
        val token = prefs.deviceToken ?: return null
        if (prefs.serverURL.isEmpty()) return null
        val rootDir = File(context.filesDir, "browser-cache")
        val b = Core.newBrowser(
            prefs.serverURL, token, rootDir.path,
            File(context.filesDir, "index.db").path, prefs.insecure,
        )
        browser = b
        return b
    }

    /**
     * Runs [block] on the shared browser, which stays open until it returns. Returns null when
     * the device is not paired — the block is not run.
     *
     * Long passes (an upload batch, a refresh) hold it for their whole duration, which is the
     * point: [close] waits for them rather than pulling the index out from under them.
     */
    fun <T> use(context: Context, block: (Browser) -> T): T? {
        val b = lock.withLock {
            val b = open(context) ?: return null
            inUse++
            b
        }
        try {
            return operations.withLock { block(b) }
        } finally {
            lock.withLock {
                inUse--
                if (inUse == 0) idle.signalAll()
            }
        }
    }

    /** True when a browser is already open — used to decide whether a refresh is needed. */
    fun isOpen(): Boolean = lock.withLock { browser != null }

    /**
     * Closes the shared browser once nothing is using it, so the next [get] builds a fresh one
     * — after re-pairing, one carrying the new device token.
     *
     * Blocks for up to [timeoutMs] waiting for work in flight, so call it off the main thread.
     * A timeout leaves the handle blocked for new borrowers. A later close can finish
     * draining it; it must never close SQLite underneath an active operation.
     */
    fun close(timeoutMs: Long = 30_000) = lock.withLock {
        closing = true
        var remaining = timeoutMs * 1_000_000
        while (inUse > 0 && remaining > 0) remaining = idle.awaitNanos(remaining)
        check(inUse == 0) { "Account operations are still finishing. Please try again." }
        browser?.close()
        browser = null
        closing = false
    }

    /**
     * Closes the browser and deletes the index behind it. Belongs to unpairing: an index that
     * outlives the pairing describes another account's — or another server's — files, and an
     * install over the top keeps app data, so it can easily outlive the folder as well.
     */
    fun wipe(context: Context) {
        close()
        val cache = File(context.filesDir, "browser-cache")
        check(!cache.exists() || cache.deleteRecursively()) { "Could not remove the previous account cache" }
        val db = File(context.filesDir, "index.db")
        // SQLite in WAL mode keeps two sidecars; leaving them behind half-restores the index.
        listOf(db, File(db.path + "-wal"), File(db.path + "-shm")).forEach {
            check(!it.exists() || it.delete()) { "Could not remove the previous account index" }
        }
    }
}
