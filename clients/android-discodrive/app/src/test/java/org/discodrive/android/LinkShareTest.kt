package org.discodrive.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class LinkShareTest {
    @Test
    fun `finds the first web link in shared text`() {
        assertEquals("https://www.threads.com/@a/post/1", LinkShare.firstWebUrl("Посмотрите https://www.threads.com/@a/post/1 !"))
        assertEquals("https://www.instagram.com/reel/ABC/?igsh=x", LinkShare.firstWebUrl("рецепт: https://www.instagram.com/reel/ABC/?igsh=x"))
        assertEquals("https://x.org/a", LinkShare.firstWebUrl("(https://x.org/a)."))
        assertNull(LinkShare.firstWebUrl("ftp://x.org/a"))
        assertNull(LinkShare.firstWebUrl("javascript:alert(1)"))
        assertNull(LinkShare.firstWebUrl("просто текст"))
    }

    @Test
    fun `what the answer means`() {
        assertEquals(LinkShare.Outcome.SAVED, LinkShare.outcome(200))
        assertEquals(LinkShare.Outcome.QUEUED, LinkShare.outcome(null))
        assertEquals(LinkShare.Outcome.QUEUED, LinkShare.outcome(503))
        assertEquals(LinkShare.Outcome.QUEUED, LinkShare.outcome(429))
        assertEquals(LinkShare.Outcome.SIGN_IN, LinkShare.outcome(401))
        assertEquals(LinkShare.Outcome.REJECTED, LinkShare.outcome(400))
    }

    @Test
    fun `title is the shared text without the link`() {
        assertEquals("Баклажаны с мисо", LinkShare.title("Баклажаны с мисо https://i.g/r/1", "https://i.g/r/1"))
        assertNull(LinkShare.title("https://i.g/r/1", "https://i.g/r/1"))
        assertNull(LinkShare.title(null, "https://i.g/r/1"))
    }

    @Test
    fun `outbox keeps order, removes, and is bounded`() {
        val box = LinkOutbox(FakePrefs())
        box.enqueue("https://a.test/1", "one")
        box.enqueue("https://a.test/2", null)
        assertEquals(listOf("https://a.test/1", "https://a.test/2"), box.pending().map { it.url })
        assertEquals("one", box.pending()[0].title)
        box.remove(box.pending()[0].id)
        assertEquals(listOf("https://a.test/2"), box.pending().map { it.url })
        for (i in 0..LinkOutbox.CAPACITY) box.enqueue("https://b.test/$i", null)
        assertEquals(LinkOutbox.CAPACITY, box.pending().size)
        assertEquals("https://b.test/1", box.pending().first().url)
    }

    @Test
    fun `a late answer takes its queued copy out`() {
        val box = LinkOutbox(FakePrefs())
        val id = box.enqueue("https://a.test/late", null)
        box.enqueue("https://a.test/other", null)
        box.remove(id)
        assertEquals(listOf("https://a.test/other"), box.pending().map { it.url })
    }

    @Test
    fun `flush sends in order and stops at the first link that cannot get through`() {
        val box = LinkOutbox(FakePrefs())
        listOf("https://a.test/1", "https://a.test/bad", "https://a.test/down", "https://a.test/2").forEach { box.enqueue(it, null) }
        val sent = mutableListOf<String>()
        LinkShare.flush(box) { url, _, addedMs ->
            assert(addedMs > 0) { "queued links carry the time they were shared" }
            sent += url
            when (url) { "https://a.test/bad" -> 400; "https://a.test/down" -> 503; else -> 200 }
        }
        assertEquals(listOf("https://a.test/1", "https://a.test/bad", "https://a.test/down"), sent)
        assertEquals(listOf("https://a.test/down", "https://a.test/2"), box.pending().map { it.url })
    }
}
