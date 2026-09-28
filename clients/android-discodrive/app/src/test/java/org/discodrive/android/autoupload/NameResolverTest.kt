package org.discodrive.android.autoupload

import org.junit.Assert.assertEquals
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
        assertEquals(Resolution.Upload("IMG_1.jpg"), NameResolver.resolve("IMG_1.jpg", fixed()))
    }

    @Test
    fun `identical content is skipped`() {
        assertEquals(Resolution.AlreadyThere, NameResolver.resolve("IMG_1.jpg", fixed("IMG_1.jpg" to EXISTS_SAME)))
    }

    @Test
    fun `taken name gets a suffix before the extension`() {
        val exists = fixed("IMG_1.jpg" to EXISTS_DIFFERENT)
        assertEquals(Resolution.Upload("IMG_1-1.jpg"), NameResolver.resolve("IMG_1.jpg", exists))
    }

    @Test
    fun `suffix keeps counting while names are taken`() {
        val exists = fixed(
            "IMG_1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-2.jpg" to EXISTS_DIFFERENT,
        )
        assertEquals(Resolution.Upload("IMG_1-3.jpg"), NameResolver.resolve("IMG_1.jpg", exists))
    }

    // A suffixed candidate that turns out to hold the very same bytes means the file is
    // already on the server under that name — uploading again would just make a third copy.
    @Test
    fun `suffixed candidate with identical content is skipped`() {
        val exists = fixed(
            "IMG_1.jpg" to EXISTS_DIFFERENT,
            "IMG_1-1.jpg" to EXISTS_SAME,
        )
        assertEquals(Resolution.AlreadyThere, NameResolver.resolve("IMG_1.jpg", exists))
    }

    @Test
    fun `name without an extension gets the suffix at the end`() {
        assertEquals(Resolution.Upload("VIDEO-1"), NameResolver.resolve("VIDEO", fixed("VIDEO" to EXISTS_DIFFERENT)))
    }

    @Test
    fun `dotfile keeps its leading dot`() {
        assertEquals(Resolution.Upload(".config-1"), NameResolver.resolve(".config", fixed(".config" to EXISTS_DIFFERENT)))
    }

    @Test
    fun `double extension only splits the last part`() {
        val exists = fixed("clip.tar.gz" to EXISTS_DIFFERENT)
        assertEquals(Resolution.Upload("clip.tar-1.gz"), NameResolver.resolve("clip.tar.gz", exists))
    }

    // Giving up is better than looping — but it is not "already uploaded": the caller
    // defers the file for a later retry instead of recording it as sent.
    @Test
    fun `gives up after the attempt cap without calling it uploaded`() {
        assertEquals(Resolution.NoFreeName, NameResolver.resolve("IMG_1.jpg", { EXISTS_DIFFERENT }))
    }
}
