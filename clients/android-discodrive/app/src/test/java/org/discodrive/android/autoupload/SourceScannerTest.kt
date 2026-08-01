package org.discodrive.android.autoupload

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class SourceScannerTest {

    @get:Rule
    val tmp = TemporaryFolder()

    private val now = 1_700_000_000_000L

    private fun file(path: String, ageMs: Long = 60_000, size: Int = 16): File {
        val f = File(tmp.root, path)
        f.parentFile?.mkdirs()
        f.writeBytes(ByteArray(size))
        f.setLastModified(now - ageMs)
        return f
    }

    private fun names(files: List<File>) = files.map { it.name }.toSet()

    @Test
    fun `picks up media in the folder`() {
        file("IMG_1.jpg")
        file("VID_1.mp4")
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg", "VID_1.mp4"), names(got))
    }

    // A file written seconds ago may still be growing — the camera is mid-save, or a
    // download is landing. The server would reject the size mismatch anyway; better not to
    // start.
    @Test
    fun `skips files that are still being written`() {
        file("IMG_old.jpg", ageMs = 60_000)
        file("IMG_fresh.jpg", ageMs = 2_000)
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_old.jpg"), names(got))
    }

    @Test
    fun `skips partial and trashed entries`() {
        file("IMG_1.jpg")
        file(".pending-1-IMG_2.jpg")
        file(".trashed-1700000000-IMG_3.jpg")
        file("IMG_4.jpg.tmp")
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg"), names(got))
    }

    @Test
    fun `skips empty files`() {
        file("IMG_1.jpg")
        file("IMG_empty.jpg", size = 0)
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg"), names(got))
    }

    @Test
    fun `mediaOnly filters non-media, allFiles keeps them`() {
        file("IMG_1.jpg")
        file("notes.txt")
        val media = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg"), names(media))
        val all = SourceScanner.scan(tmp.root, mediaOnly = false, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg", "notes.txt"), names(all))
    }

    @Test
    fun `subfolders are honoured both ways`() {
        file("IMG_1.jpg")
        file("trip/IMG_2.jpg")
        val flat = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(setOf("IMG_1.jpg"), names(flat))
        val deep = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = true, now = now)
        assertEquals(setOf("IMG_1.jpg", "IMG_2.jpg"), names(deep))
    }

    @Test
    fun `hidden thumbnail folders are never walked`() {
        file("IMG_1.jpg")
        file(".thumbnails/thumb.jpg")
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = true, now = now)
        assertEquals(setOf("IMG_1.jpg"), names(got))
    }

    // The photo just taken matters more than the archive: it should be at the head of the
    // queue, not behind thousands of older files.
    @Test
    fun `newest files come first`() {
        file("old.jpg", ageMs = 900_000)
        file("newest.jpg", ageMs = 30_000)
        file("middle.jpg", ageMs = 300_000)
        val got = SourceScanner.scan(tmp.root, mediaOnly = true, includeSubfolders = false, now = now)
        assertEquals(listOf("newest.jpg", "middle.jpg", "old.jpg"), got.map { it.name })
    }

    @Test
    fun `missing folder scans to nothing instead of throwing`() {
        val got = SourceScanner.scan(File(tmp.root, "nope"), mediaOnly = true, includeSubfolders = true, now = now)
        assertTrue(got.isEmpty())
    }

    @Test
    fun `media detection covers common camera formats and rejects sidecars`() {
        listOf("a.jpg", "b.jpeg", "c.png", "d.heic", "e.mp4", "f.mov", "g.dng", "h.webp", "i.gif")
            .forEach { assertTrue(it, SourceScanner.isMedia(it)) }
        listOf("a.txt", "b.xmp", "c.aae", "d.pdf", "noextension")
            .forEach { assertFalse(it, SourceScanner.isMedia(it)) }
    }
}
