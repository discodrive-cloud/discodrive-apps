package org.discodrive.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TrashBlockedTest {
    @Test
    fun `the core's refused-purge error is recognised`() {
        val msg = "trash kept: a folder still holds items that are not in the trash (/files/trash: 409: {\"error\":\"folder holds items\"})"
        assertTrue(TrashBlocked.matches(msg))
        assertEquals("Explained.", TrashBlocked.describe(msg, "Explained."))
    }

    @Test
    fun `other errors pass through unchanged`() {
        val other = "/files/trash: 500"
        assertFalse(TrashBlocked.matches(other))
        assertEquals(other, TrashBlocked.describe(other, "Explained."))
    }
}
