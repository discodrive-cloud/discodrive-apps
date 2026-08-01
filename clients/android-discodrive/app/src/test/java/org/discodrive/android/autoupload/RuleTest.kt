package org.discodrive.android.autoupload

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

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
