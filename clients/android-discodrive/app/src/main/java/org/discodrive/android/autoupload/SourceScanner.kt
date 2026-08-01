package org.discodrive.android.autoupload

import java.io.File
import java.util.Locale

/**
 * Finds the files in a source folder that are worth uploading.
 *
 * Pure over a [File] tree so it can be unit-tested on the JVM: no Android types, no
 * journal, no network. Deciding what was already sent is the journal's job — this only
 * answers "what is there, and is it finished being written".
 */
object SourceScanner {
    /**
     * A file touched within this window may still be growing (the camera is saving, a
     * download is landing). The server rejects a size that shifts mid-upload, so waiting a
     * pass is cheaper than a failed transfer.
     */
    const val SETTLE_MS = 10_000L

    private val MEDIA_EXTENSIONS = setOf(
        "jpg", "jpeg", "png", "heic", "heif", "webp", "gif", "avif", "bmp",
        "dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2", "tif", "tiff",
        "mp4", "mov", "m4v", "3gp", "mkv", "webm", "avi",
    )

    /** Names that mean "not a finished file" on Android's media storage. */
    private fun isPartial(name: String): Boolean =
        name.startsWith(".pending") ||
            name.startsWith(".trashed-") ||
            name.endsWith(".tmp") ||
            name.endsWith(".part") ||
            name.endsWith(".crdownload")

    fun isMedia(name: String): Boolean {
        val dot = name.lastIndexOf('.')
        if (dot <= 0 || dot == name.length - 1) return false
        return name.substring(dot + 1).lowercase(Locale.US) in MEDIA_EXTENSIONS
    }

    /**
     * Returns the candidates in [dir], newest first — a photo just taken should not queue
     * behind an archive of thousands.
     *
     * A missing or unreadable folder scans to an empty list rather than throwing: the
     * source may be on removable storage, and a background pass should not crash over it.
     */
    fun scan(dir: File, mediaOnly: Boolean, includeSubfolders: Boolean, now: Long): List<File> {
        val out = ArrayList<File>()
        collect(dir, mediaOnly, includeSubfolders, now, out, depth = 0)
        out.sortByDescending { it.lastModified() }
        return out
    }

    /** Guards against a symlink loop or a pathologically deep tree in a background pass. */
    private const val MAX_DEPTH = 16

    private fun collect(
        dir: File,
        mediaOnly: Boolean,
        includeSubfolders: Boolean,
        now: Long,
        out: MutableList<File>,
        depth: Int,
    ) {
        if (depth > MAX_DEPTH) return
        val entries = dir.listFiles() ?: return
        for (f in entries) {
            val name = f.name
            if (f.isDirectory) {
                // Hidden folders hold thumbnails, caches and Android's trash; none of it
                // is the user's content.
                if (includeSubfolders && !name.startsWith(".")) {
                    collect(f, mediaOnly, includeSubfolders, now, out, depth + 1)
                }
                continue
            }
            if (isPartial(name)) continue
            if (f.length() == 0L) continue
            if (now - f.lastModified() < SETTLE_MS) continue
            if (mediaOnly && !isMedia(name)) continue
            out.add(f)
        }
    }
}
