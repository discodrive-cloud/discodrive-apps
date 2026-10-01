package org.discodrive.android

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FileNamesTest {

    // The name comes from another app's content provider: it is whatever that app says.
    @Test
    fun `ordinary names pass`() {
        assertTrue(isValidUploadName("IMG_0001.jpg"))
        assertTrue(isValidUploadName("отчёт 2026.pdf"))
        assertTrue(isValidUploadName("..hidden"))
        assertTrue(isValidUploadName("a".repeat(255)))
    }

    @Test
    fun `names that escape the folder or say nothing are refused`() {
        assertFalse(isValidUploadName(""))
        assertFalse(isValidUploadName("   "))
        assertFalse(isValidUploadName("."))
        assertFalse(isValidUploadName(".."))
        assertFalse(isValidUploadName("../secret"))
        assertFalse(isValidUploadName("a/b"))
        assertFalse(isValidUploadName("a\\b"))
        assertFalse(isValidUploadName("a".repeat(256)))
    }
}
