package org.discodrive.fastsync

import android.content.Context
import android.os.Environment
import mobile.Client
import java.io.File
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * The process-wide sync [Client].
 *
 * The local index is a SQLite file and tolerates one writer. The screen and the background
 * worker each used to build their own client over the same file, which is a second writer
 * waiting to collide — and now that a manual sync runs in the worker too, they would routinely
 * run at once. Both go through this.
 *
 * Borrow it with [use]; [close] belongs to unpairing and waits for work in flight.
 */
object ClientHolder {

    private val lock = ReentrantLock()
    private val idle = lock.newCondition()

    private var client: Client? = null
    private var inUse = 0

    val syncDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDriveFastSync/Sync")

    /** Opens the client from the saved profile, or returns null when not paired yet. */
    fun get(context: Context): Client? = lock.withLock { open(context) }

    private fun open(context: Context): Client? {
        client?.let { return it }
        val prefs = Prefs(context)
        val token = prefs.deviceToken ?: return null
        if (prefs.serverURL.isEmpty()) return null
        syncDir.mkdirs()
        val db = File(context.filesDir, "state.db").path
        val c = SyncCore.newClient(prefs.serverURL, token, syncDir.path, db, prefs.insecure)
        client = c
        return c
    }

    /**
     * Runs [block] on the shared client, which stays open until it returns. Null when the
     * device is not paired — the block is not run.
     */
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

    /**
     * Closes the shared client once nothing is using it. Blocks up to [timeoutMs] waiting for
     * work in flight, so call it off the main thread.
     */
    fun close(timeoutMs: Long = 30_000) = lock.withLock {
        var remaining = timeoutMs * 1_000_000 // awaitNanos takes nanoseconds
        while (inUse > 0 && remaining > 0) {
            remaining = idle.awaitNanos(remaining)
        }
        runCatching { client?.close() }
        client = null
    }

    /**
     * Closes the client and deletes the index behind it. Belongs to unpairing.
     *
     * Leaving it in place is what turned a re-pairing into a disaster: the index outlived both
     * the unpair and the sync folder — an install over the top keeps app data — so the next
     * pass saw every file it knew as locally deleted and deleted them on the server too.
     */
    fun wipe(context: Context) {
        close()
        val db = File(context.filesDir, "state.db")
        // SQLite in WAL mode keeps two sidecars; leaving them turns the next open into a
        // half-restored index rather than a fresh one.
        listOf(db, File(db.path + "-wal"), File(db.path + "-shm")).forEach { it.delete() }
    }
}
