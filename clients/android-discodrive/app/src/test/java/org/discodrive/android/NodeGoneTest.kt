package org.discodrive.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NodeGoneTest {
    private val gone = "node no longer exists on the server (PATCH rename: 404: {\"error\":\"not found\"})"

    @Test
    fun `the core's gone error is recognised`() {
        assertTrue(NodeGone.matches(gone))
        assertEquals("Gone.", NodeGone.describe(gone, "Gone."))
    }

    @Test
    fun `other errors pass through unchanged`() {
        val other = "PATCH rename: 409: {\"error\":\"name already taken in this folder\"}"
        assertFalse(NodeGone.matches(other))
        assertEquals(other, NodeGone.describe(other, "Gone."))
        assertFalse(NodeGone.matches(null))
        assertNull(NodeGone.describe(null, "Gone."))
    }
}
