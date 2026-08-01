package org.discodrive.android.autoupload

import android.os.Build
import android.os.Environment
import mobile.Browser
import org.discodrive.android.Core
import org.discodrive.android.Prefs
import java.io.File
import java.security.MessageDigest

/** Outcome of one pass, for the notification and the UI. */
data class RunResult(
    val uploaded: Int,
    val skipped: Int,
    val deferred: Int,
    val blocked: Block = Block.NONE,
    val error: String? = null,
)

/**
 * One pass of auto-upload: scan the source folder, decide a name for each new file, send it,
 * record it.
 *
 * P1 is deliberately one hard-wired rule — the camera folder into
 * `/Camera Uploads/<device>`, media only, newest first, existing files left alone. Multiple
 * rules and their editor come later; everything here is written so that adding them means
 * looping over rules rather than rewriting the pass.
 *
 * Local files are only ever read. Nothing is deleted, moved or renamed on the phone.
 */
class AutoUploadRunner(
    private val browser: Browser,
    private val journal: UploadJournal,
    private val prefs: Prefs,
) {
    companion object {
        const val DEST_ROOT = "Camera Uploads"
        val sourceDir: File
            get() = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DCIM), "Camera")

        /** Falls back to the model when the marketing name is missing or a duplicate. */
        val deviceFolder: String
            get() = (Build.MODEL ?: "Android").trim().ifEmpty { "Android" }

        /** Give up on a file after this many failed passes; it stays visible in the log. */
        const val MAX_ATTEMPTS = 5
    }

    /**
     * Records the current contents of the source folder as pre-existing, so switching the
     * feature on does not push a years-old archive over mobile data. Runs once.
     */
    fun seedIfNeeded(): Int {
        if (prefs.autoUploadSeeded) return 0
        val existing = SourceScanner.scan(sourceDir, mediaOnly = true, includeSubfolders = false, now = Long.MAX_VALUE)
        journal.seedPreexisting(existing)
        prefs.autoUploadSeeded = true
        return existing.size
    }

    /**
     * Uploads everything new. [progress] is called before each file with (done, total, name).
     * [isCancelled] is polled between files so the service can stop promptly.
     */
    fun runOnce(
        progress: (done: Int, total: Int, name: String) -> Unit = { _, _, _ -> },
        isCancelled: () -> Boolean = { false },
    ): RunResult {
        val destID = try {
            resolveDestination()
        } catch (e: Exception) {
            return RunResult(0, 0, 0, error = e.message ?: "cannot resolve the destination folder")
        }

        val candidates = SourceScanner.scan(
            sourceDir, mediaOnly = true, includeSubfolders = false, now = System.currentTimeMillis(),
        ).filterNot { journal.isKnown(it) }

        var uploaded = 0
        var skipped = 0
        var deferred = 0
        for ((i, file) in candidates.withIndex()) {
            if (isCancelled()) break
            progress(i, candidates.size, file.name)
            when (uploadOne(file, destID)) {
                Outcome.UPLOADED -> uploaded++
                Outcome.SKIPPED -> skipped++
                Outcome.DEFERRED -> deferred++
            }
        }
        // One refresh for the whole batch: UploadAs deliberately leaves the index alone.
        if (uploaded > 0) runCatching { browser.refresh() }
        return RunResult(uploaded, skipped, deferred)
    }

    private enum class Outcome { UPLOADED, SKIPPED, DEFERRED }

    private fun uploadOne(file: File, destID: String): Outcome {
        if (journal.attempts(file) >= MAX_ATTEMPTS) return Outcome.DEFERRED
        return try {
            val sha = sha256(file)
            val name = NameResolver.resolve(file.name) { candidate ->
                Core.existsWithHash(browser, destID, candidate, sha)
            }
            if (name == null) {
                // Already on the server byte for byte (or no free name): record it so the
                // next pass does not hash it again.
                journal.markSent(file, serverName = file.name, sha = sha)
                return Outcome.SKIPPED
            }
            Core.uploadAs(browser, file.path, destID, name)
            journal.markSent(file, serverName = name, sha = sha)
            Outcome.UPLOADED
        } catch (e: Exception) {
            val msg = e.message ?: e.toString()
            if (msg.contains(Core.SIZE_MISMATCH)) {
                // The file grew while it was being sent — a video still recording, most
                // likely. Its mtime moved too, so the next pass treats it as a fresh file
                // and picks it up once it has settled.
                journal.markDeferred(file, "changed while uploading")
            } else {
                journal.markDeferred(file, msg.take(300))
            }
            Outcome.DEFERRED
        }
    }

    /** `/Camera Uploads/<device>`, created on first use and cached by node id afterwards. */
    private fun resolveDestination(): String {
        prefs.autoUploadDestID?.let { return it }
        val root = Core.ensureFolder(browser, "", DEST_ROOT)
        val dest = Core.ensureFolder(browser, root, deviceFolder)
        prefs.autoUploadDestID = dest
        return dest
    }

    private fun sha256(file: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = input.read(buf)
                if (n <= 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }
}
