package org.discodrive.android.sync

import android.content.Context
import android.os.Environment
import mobile.Client
import mobile.Mobile
import org.discodrive.android.Prefs
import java.io.File
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * The process-wide folder-sync [Client] — the engine that mirrors the folder chosen on the
 * server into [syncDir], the same one the desktop daemon and Fast Sync run.
 *
 * Its index is a SQLite file that tolerates one writer, so the screen, the worker and the
 * event listener all borrow this one through [use]; [close] belongs to switching the sync
 * off and to unpairing, and waits for a pass in flight.
 *
 * The folder is separate from the browser's private cache: the engine deletes
 * whatever under its root is not in its index, and the browser's cache would be gone.
 */
object SyncHolder {

    private val lock = ReentrantLock()
    private val idle = lock.newCondition()

    private var client: Client? = null
    private var inUse = 0
    private var closing = false

    val syncDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDriveSync")

    private fun dbFile(context: Context) = File(context.filesDir, "sync-state.db")

    /** Opens the client from the saved pairing, or returns null when not paired. */
    fun get(context: Context): Client? = lock.withLock { open(context) }

    private fun open(context: Context): Client? {
        if (closing) return null
        val prefs = Prefs(context)
        if (prefs.unpairing) return null
        client?.let { return it }
        val token = prefs.deviceToken ?: return null
        if (prefs.serverURL.isEmpty()) return null
        syncDir.mkdirs()
        val c = Mobile.new_(prefs.serverURL, token, syncDir.path, dbFile(context).path, prefs.insecure)
        client = c
        return c
    }

    /** Runs [block] on the shared client, kept open until it returns; null when not paired. */
    fun <T> use(context: Context, block: (Client) -> T): T? {
        val c = lock.withLock {
            val c = open(context) ?: return null
            inUse++
            c
        }
        try {
            return block(c)
        } finally {
            lock.withLock {
                inUse--
                if (inUse == 0) idle.signalAll()
            }
        }
    }

    /** Closes the client once nothing is using it; blocks up to [timeoutMs], so call it off the main thread. */
    fun close(timeoutMs: Long = 30_000) = lock.withLock {
        closing = true
        client?.cancel()
        var remaining = timeoutMs * 1_000_000
        while (inUse > 0 && remaining > 0) remaining = idle.awaitNanos(remaining)
        check(inUse == 0) { "Account operations are still finishing. Please try again." }
        client?.close()
        client = null
        closing = false
    }

    /**
     * Closes the client and deletes its index. Belongs to unpairing: an index that outlives a
     * pairing describes files this device no longer has any claim to. The folder itself is
     * left where it is — the next pairing sets it aside, never uploads it.
     */
    fun wipe(context: Context) {
        close()
        val db = dbFile(context)
        listOf(db, File(db.path + "-wal"), File(db.path + "-shm")).forEach {
            check(!it.exists() || it.delete()) { "Could not remove the previous account index" }
        }
    }
}
