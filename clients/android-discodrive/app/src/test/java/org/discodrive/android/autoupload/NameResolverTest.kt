package org.discodrive.android.autoupload

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

// The server takes a same-named upload as a new version of the existing file, so the name a
// photo lands under is decided here, before anything is sent.
class NameResolverTest {

    private fun fixed(vararg answers: Pair<String, String>): (String) -> String {
        val m = answers.toMap()
        return { name -> m[name] ?: EXISTS_ABSENT }
    }

    @Test
    fun `free name is used as is`() {
        assertEquals("IMG_1.jpg", NameResolver.resolve("IMG_1.jpg", fixed()))
    }

    @Test
    fun `identical content is skipped`() {
        assertNull(NameResolver.resolve("IMG_1.jpg", fixed("IMG_1.jpg" to EXISTS_SAME)))
    }

    @Test
    fun `taken name gets a suffix before the extension`() {
        val exists = fixed("IMG_1.jpg" to EXISTS_DIFFERENT)
        assertEquals("IMG_1-1.jpg", NameResolver.resolve("IMG_1.jpg", exists))
    }

    @Test
    fun `suffix keeps counting while names are taken`() {
        val exists = fixed(
            "IMG_1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-2.jpg" to EXISTS_DIFFERENT,
        )
        assertEquals("IMG_1-3.jpg", NameResolver.resolve("IMG_1.jpg", exists))
    }

    // A suffixed candidate that turns out to hold the very same bytes means the file is
    // already on the server under that name — uploading again would just make a third copy.
    @Test
    fun `suffixed candidate with identical content is skipped`() {
        val exists = fixed(
            "IMG_1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-1.jpg" to EXISTS_SAME,
        )
        assertNull(NameResolver.resolve("IMG_1.jpg", exists))
    }

    @Test
    fun `name without an extension gets the suffix at the end`() {
        assertEquals("VIDEO-1", NameResolver.resolve("VIDEO", fixed("VIDEO" to EXISTS_DIFFERENT)))
    }

    @Test
    fun `dotfile keeps its leading dot`() {
        assertEquals(".config-1", NameResolver.resolve(".config", fixed(".config" to EXISTS_DIFFERENT)))
    }

    @Test
    fun `double extension only splits the last part`() {
        val exists = fixed("clip.tar.gz" to EXISTS_DIFFERENT)
        assertEquals("clip.tar-1.gz", NameResolver.resolve("clip.tar.gz", exists))
    }

    // Giving up is better than looping: something is wrong with the destination, and the
    // caller logs the file as deferred instead of spinning.
    @Test
    fun `gives up after the attempt cap`() {
        assertNull(NameResolver.resolve("IMG_1.jpg", { EXISTS_DIFFERENT }))
    }
}
