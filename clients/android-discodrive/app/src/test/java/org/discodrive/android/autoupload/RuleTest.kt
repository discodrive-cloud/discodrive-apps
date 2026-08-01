package org.discodrive.android.autoupload

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class RuleTest {

    private val camera = Rule(
        sourcePath = "/storage/emulated/0/DCIM/Camera",
        destSegments = listOf("Camera Uploads", "Pixel 8"),
    )
    private val downloads = Rule(
        sourcePath = "/storage/emulated/0/Download",
        destSegments = listOf("Inbox", "Phone"),
        mediaOnly = false,
        includeSubfolders = true,
        seeded = true,
        destID = "node-9",
    )

    @Test
    fun `a list survives a round trip`() {
        val back = Rule.listFromJson(Rule.listToJson(listOf(camera, downloads)))
        assertEquals(listOf(camera, downloads), back)
    }

    @Test
    fun `labels read the way the UI shows them`() {
        assertEquals("Camera", camera.sourceLabel)
        assertEquals("/Camera Uploads/Pixel 8", camera.destLabel)
    }

    // Rules are user data: a corrupt preference must cost the folder list, not the app.
    @Test
    fun `garbage storage yields no rules instead of throwing`() {
        assertTrue(Rule.listFromJson("{not json").isEmpty())
        assertTrue(Rule.listFromJson("").isEmpty())
        assertTrue(Rule.listFromJson(null).isEmpty())
    }

    // /sdcard is a symlink to /storage/emulated/0 on a device; storing both spellings would
    // let the same folder be added twice and the same photo be uploaded twice.
    @Test
    fun `paths are normalized when a rule is built`() {
        val r = Rule.of("/tmp/../tmp/photos", listOf("X"))
        assertEquals(Rule.normalize("/tmp/photos"), r.sourcePath)
    }

    @Test
    fun `normalize survives a path that does not exist`() {
        assertTrue(Rule.normalize("/definitely/not/here").endsWith("/definitely/not/here"))
    }

    // A picture folder is filtered to media; anything the user picks by hand is not — adding
    // Documents and getting only the screenshots in it would be nonsense.
    @Test
    fun `media filter follows the folder, not a switch`() {
        assertTrue(AutoUploadRunner.defaultMediaOnlyFor(File("/storage/emulated/0/DCIM/Camera")))
        assertTrue(AutoUploadRunner.defaultMediaOnlyFor(File("/storage/emulated/0/Pictures")))
        assertTrue(AutoUploadRunner.defaultMediaOnlyFor(File("/storage/emulated/0/Pictures/Screenshots")))
        assertTrue(!AutoUploadRunner.defaultMediaOnlyFor(File("/storage/emulated/0/Documents")))
        assertTrue(!AutoUploadRunner.defaultMediaOnlyFor(File("/storage/emulated/0/Download")))
    }

    @Test
    fun `defaults match the conservative choice`() {
        // Media only, no subfolders, and NOT seeded: an unseeded rule must not upload an
        // existing archive before it has been recorded.
        assertTrue(camera.mediaOnly)
        assertTrue(!camera.includeSubfolders)
        assertTrue(!camera.seeded)
        assertTrue(camera.enabled)
    }
}
