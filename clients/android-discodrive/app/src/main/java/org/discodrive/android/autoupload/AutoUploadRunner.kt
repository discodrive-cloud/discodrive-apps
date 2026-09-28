package org.discodrive.android.autoupload

import android.os.Build
import android.os.Environment
import mobile.Browser
import org.discodrive.android.Core
import org.discodrive.android.Prefs
import java.io.File
import java.security.MessageDigest

/** Outcome of one pass, summed over every rule. */
data class RunResult(
    val uploaded: Int,
    val skipped: Int,
    val deferred: Int,
    val blocked: Block = Block.NONE,
    val error: String? = null,
)

/**
 * One pass of auto-upload: for every enabled rule, scan its folder, decide a name for each
 * new file, send it, record it.
 *
 * Local files are only ever read. Nothing is deleted, moved or renamed on the phone.
 */
class AutoUploadRunner(
    private val browser: Browser,
    private val journal: UploadJournal,
    private val prefs: Prefs,
) {
    companion object {
        const val DEST_ROOT = "DeviceUploads"

        /** Falls back when the model is missing; also what the destination folder is named. */
        val deviceFolder: String
            get() = (Build.MODEL ?: "Android").trim().ifEmpty { "Android" }

        val cameraDir: File
            get() = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DCIM), "Camera")

        /** The rule the app proposes on first run; the user can add or remove any other. */
        fun defaultRule(): Rule = Rule.of(
            sourcePath = cameraDir.path,
            destSegments = listOf(DEST_ROOT, deviceFolder),
        )

        /**
         * Where a freshly picked folder goes by default: under the device folder, keeping
         * its own name, so two phones and two folders never land on top of each other.
         */
        fun defaultDestFor(folder: File): List<String> =
            listOf(DEST_ROOT, deviceFolder, folder.name.ifEmpty { "Folder" })

        /**
         * Whether a folder should be filtered down to photos and videos.
         *
         * Only picture folders: someone who adds Documents or Download wants what is in
         * there, not the two screenshots that happen to sit among the PDFs.
         */
        fun defaultMediaOnlyFor(folder: File): Boolean {
            val p = Rule.normalize(folder.path)
            return p.contains("/DCIM") || p.endsWith("/Pictures") || p.contains("/Pictures/")
        }

        /**
         * After this many failed passes a file waits [RETRY_AFTER_MS] before the next try,
         * instead of costing an attempt on every pass. It stays visible in the log.
         */
        const val MAX_ATTEMPTS = 5
        const val RETRY_AFTER_MS = 24 * 60 * 60 * 1000L
    }

    /**
     * Records the current contents of any not-yet-seeded rule as pre-existing, so switching
     * a folder on does not push its archive over mobile data. Returns how many files were
     * marked. Runs once per rule.
     */
    fun seedIfNeeded(): Int {
        var marked = 0
        val seeded = HashSet<String>()
        for (rule in prefs.rules.filterNot { it.seeded }) {
            val existing = SourceScanner.scan(
                rule.source, rule.mediaOnly, rule.includeSubfolders, now = Long.MAX_VALUE,
            )
            journal.seedPreexisting(existing)
            marked += existing.size
            seeded.add(rule.sourcePath)
        }
        // Only the flag is written back, onto the list as it is now: the scan can take
        // seconds, and a folder added or removed meanwhile must stay added or removed.
        if (seeded.isNotEmpty()) {
            prefs.updateRules { current -> current.map { if (it.sourcePath in seeded) it.copy(seeded = true) else it } }
        }
        return marked
    }

    /**
     * Uploads everything new across all enabled rules. [progress] is called before each file
     * with (done, total, name); [isCancelled] is polled between files.
     */
    fun runOnce(
        progress: (done: Int, total: Int, name: String) -> Unit = { _, _, _ -> },
        isCancelled: () -> Boolean = { false },
    ): RunResult {
        // Pull the change feed BEFORE deciding any names. The collision check reads the local
        // index, so a file added from another device or the web UI since the last refresh
        // would read as "absent" — and uploading under that name replaces it with a new
        // version. Verified on a device: without this, an existing server file was silently
        // overwritten.
        try {
            browser.refresh()
        } catch (e: Exception) {
            return RunResult(0, 0, 0, error = "cannot reach the server: ${e.message}")
        }

        val rules = prefs.rules.filter { it.enabled }
        if (rules.isEmpty()) return RunResult(0, 0, 0)

        // Work out the whole batch first so the notification can show a real total instead
        // of counting up per folder.
        val now = System.currentTimeMillis()
        val work = rules.mapNotNull { rule ->
            val files = SourceScanner.scan(rule.source, rule.mediaOnly, rule.includeSubfolders, now)
                .filterNot { journal.isKnown(it) }
            if (files.isEmpty()) null else rule to files
        }
        val total = work.sumOf { it.second.size }

        var uploaded = 0
        var skipped = 0
        var deferred = 0
        var done = 0
        var lastError: String? = null
        // Names taken earlier in this batch, per destination. The index is refreshed once at
        // the end, so without this a later file could be handed a name the batch already used
        // and would overwrite it.
        val claimed = HashMap<String, MutableSet<String>>()

        for ((rule, files) in work) {
            if (isCancelled()) break
            val destID = try {
                resolveDestination(rule)
            } catch (e: Exception) {
                // One unreachable destination must not stop the other folders.
                lastError = "${rule.sourceLabel}: ${e.message}"
                continue
            }
            val taken = claimed.getOrPut(destID) { HashSet() }
            for (file in files) {
                if (isCancelled()) break
                progress(done, total, file.name)
                done++
                when (uploadOne(file, destID, taken)) {
                    Outcome.UPLOADED -> uploaded++
                    Outcome.SKIPPED -> skipped++
                    Outcome.DEFERRED -> deferred++
                }
            }
        }
        // One refresh for the whole batch: UploadAs deliberately leaves the index alone.
        if (uploaded > 0) runCatching { browser.refresh() }
        return RunResult(uploaded, skipped, deferred, error = lastError)
    }

    private enum class Outcome { UPLOADED, SKIPPED, DEFERRED }

    private fun uploadOne(file: File, destID: String, claimed: MutableSet<String>): Outcome {
        if (!journal.mayRetry(file, MAX_ATTEMPTS, RETRY_AFTER_MS)) return Outcome.DEFERRED
        // Snapshot the identity BEFORE reading the file: if it changes while it is being
        // sent, the journal must remember the version that actually went up, so the new one
        // still counts as new work.
        val size = file.length()
        val mtime = file.lastModified()
        return try {
            val sha = sha256(file)
            val resolution = NameResolver.resolve(file.name) { candidate ->
                // A name this batch already used is taken, even though the index — refreshed
                // only at the end — still reports it free.
                if (candidate in claimed) EXISTS_DIFFERENT
                else Core.existsWithHash(browser, destID, candidate, sha)
            }
            val name = when (resolution) {
                is Resolution.Upload -> resolution.name
                Resolution.AlreadyThere -> {
                    // Already on the server byte for byte: record it so the next pass does
                    // not hash it again.
                    journal.markSent(file, serverName = file.name, sha = sha, size = size, mtime = mtime)
                    return Outcome.SKIPPED
                }
                Resolution.NoFreeName -> {
                    journal.markDeferred(file, "no free name in the destination folder")
                    return Outcome.DEFERRED
                }
            }
            Core.uploadAs(browser, file.path, destID, name)
            claimed.add(name)
            journal.markSent(file, serverName = name, sha = sha, size = size, mtime = mtime)
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
                // The destination may have been deleted or moved on the server: resolve it
                // again next pass (creating a folder that exists is a no-op).
                forgetDestination(destID)
            }
            Outcome.DEFERRED
        }
    }

    private fun forgetDestination(destID: String) {
        prefs.updateRules { current -> current.map { if (it.destID == destID) it.copy(destID = null) else it } }
    }

    /** Resolves (creating on first use) the rule's destination, caching the node id. */
    private fun resolveDestination(rule: Rule): String {
        rule.destID?.let { return it }
        var parent = ""
        for (segment in rule.destSegments) {
            parent = Core.ensureFolder(browser, parent, segment)
        }
        prefs.updateRules { current -> current.map { if (it.sourcePath == rule.sourcePath) it.copy(destID = parent) else it } }
        return parent
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
