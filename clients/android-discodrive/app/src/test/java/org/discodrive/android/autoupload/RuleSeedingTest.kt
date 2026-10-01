package org.discodrive.android.autoupload

import org.discodrive.android.FakePrefs
import org.discodrive.android.Prefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

class RuleSeedingTest {

    private val rule = Rule(sourcePath = "/storage/emulated/0/DCIM/Camera", destSegments = listOf("DeviceUploads", "Pixel"))
    private val photo = File("/storage/emulated/0/DCIM/Camera/a.jpg")

    private fun paired(): Prefs = Prefs(FakePrefs()).also { it.saveServer("https://old.example", "old-token", "") }

    /** The unpair cleanup as BrowserViewModel runs it: journal and prefs go under the generation lock. */
    private fun unpair(prefs: Prefs, journal: MutableList<File>) = PairingGeneration.end {
        journal.clear()
        prefs.clear()
    }

    /** A scanner that holds until released, reporting when it has started. */
    private class GatedScanner(private val result: List<File>) {
        val started = CountDownLatch(1)
        val release = CountDownLatch(1)
        fun scan(@Suppress("UNUSED_PARAMETER") r: Rule): List<File> {
            started.countDown()
            check(release.await(10, TimeUnit.SECONDS))
            return result
        }
    }

    @Test
    fun `a folder added while paired is seeded and stored`() {
        val prefs = paired()
        val journal = mutableListOf<File>()
        assertTrue(RuleSeeding.add(prefs, rule, { listOf(photo) }, { journal += it }))
        assertEquals(listOf(photo), journal)
        assertEquals(listOf(rule.copy(seeded = true)), prefs.rules)
    }

    @Test
    fun `a scan that outlives an unpair writes neither the journal nor the rule`() {
        val prefs = paired()
        val journal = mutableListOf<File>()
        val scanner = GatedScanner(listOf(photo))
        var added: Boolean? = null
        val adding = thread { added = RuleSeeding.add(prefs, rule, scanner::scan) { journal += it } }
        assertTrue(scanner.started.await(10, TimeUnit.SECONDS))
        unpair(prefs, journal)
        scanner.release.countDown()
        adding.join(10_000)

        assertEquals(false, added)
        assertTrue("the old account's journal is not rewritten", journal.isEmpty())
        assertTrue("the old account's rule is not put back", prefs.rules.isEmpty())
    }

    @Test
    fun `a scan that outlives an unpair and a new pairing leaves the new pairing alone`() {
        val prefs = paired()
        val journal = mutableListOf<File>()
        val scanner = GatedScanner(listOf(photo))
        var added: Boolean? = null
        val adding = thread { added = RuleSeeding.add(prefs, rule, scanner::scan) { journal += it } }
        assertTrue(scanner.started.await(10, TimeUnit.SECONDS))
        unpair(prefs, journal)
        prefs.saveServer("https://new.example", "new-token", "")
        scanner.release.countDown()
        adding.join(10_000)

        assertEquals(false, added)
        assertTrue(journal.isEmpty())
        assertTrue("the next pairing does not inherit the source", prefs.rules.isEmpty())
    }

    @Test
    fun `nothing is stored while an unpair is under way`() {
        val prefs = paired()
        prefs.unpairing = true
        val journal = mutableListOf<File>()
        assertFalse(RuleSeeding.add(prefs, rule, { listOf(photo) }, { journal += it }))
        assertTrue(journal.isEmpty())
        assertTrue(prefs.rules.isEmpty())
    }

    @Test
    fun `a folder already a rule is not seeded again`() {
        val prefs = paired()
        prefs.addRule(rule)
        val journal = mutableListOf<File>()
        assertFalse(RuleSeeding.add(prefs, rule, { listOf(photo) }, { journal += it }))
        assertTrue(journal.isEmpty())
    }
}
