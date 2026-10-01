package org.discodrive.android.autoupload

import org.discodrive.android.FakePrefs
import org.discodrive.android.Prefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * The auto-upload worker opens the journal at the start of a pass and writes to it for as
 * long as the pass runs. Its writes go through a [PairingGate] taken at that start, so a
 * pass still running when the device is unpaired cannot write the old account's entries
 * into the journal (or its destinations into the rules) after the cleanup.
 */
class PairingGateTest {

    private fun paired(): Prefs = Prefs(FakePrefs()).also { it.saveServer("https://old.example", "old-token", "") }

    @Test
    fun `a write while the pairing is in place goes through`() {
        val gate = PairingGate(paired())
        assertEquals(42, gate.write { 42 })
    }

    @Test
    fun `a pass that outlives an unpair writes nothing afterwards`() {
        val prefs = paired()
        val gate = PairingGate(prefs)           // the worker starts its pass
        val journal = mutableListOf("old row")
        PairingGeneration.end { journal.clear(); prefs.clear() }

        assertNull(gate.write { journal += "late row" })
        assertTrue(journal.isEmpty())
    }

    @Test
    fun `a pass that outlives an unpair and a new pairing leaves the new pairing alone`() {
        val prefs = paired()
        val gate = PairingGate(prefs)
        PairingGeneration.end { prefs.clear() }
        prefs.saveServer("https://new.example", "new-token", "")

        var wrote = false
        assertNull(gate.write { wrote = true })
        assertFalse(wrote)
    }

    @Test
    fun `nothing is written while an unpair is under way`() {
        val prefs = paired()
        val gate = PairingGate(prefs)
        prefs.unpairing = true
        var wrote = false
        assertNull(gate.write { wrote = true })
        assertFalse(wrote)
    }

    @Test
    fun `the cleanup waits for a write already under way, never the other way round`() {
        val prefs = paired()
        val gate = PairingGate(prefs)
        val order = Collections.synchronizedList(mutableListOf<String>())
        val inWrite = CountDownLatch(1)
        val finishWrite = CountDownLatch(1)

        val writer = thread {
            gate.write {
                inWrite.countDown()
                check(finishWrite.await(10, TimeUnit.SECONDS))
                order += "write"
            }
        }
        assertTrue(inWrite.await(10, TimeUnit.SECONDS))
        val cleanupDone = CountDownLatch(1)
        val cleaner = thread { PairingGeneration.end { order += "wipe" }; cleanupDone.countDown() }
        assertFalse("the wipe must not run in the middle of a write", cleanupDone.await(300, TimeUnit.MILLISECONDS))
        finishWrite.countDown()
        writer.join(10_000); cleaner.join(10_000)

        assertEquals(listOf("write", "wipe"), order)
        assertNull("and after the wipe the same pass writes nothing", gate.write { order += "late" })
    }

    // The gate does not rely on every unpair path ending the generation: a new pairing
    // starts a new one by itself.
    @Test
    fun `a new pairing alone shuts out a gate taken under the old one`() {
        val prefs = paired()
        val gate = PairingGate(prefs)
        prefs.clear()                            // an unpair that did not go through PairingGeneration.end
        prefs.saveServer("https://new.example", "new-token", "")

        var wrote = false
        assertNull(gate.write { wrote = true })
        assertFalse(wrote)
        assertEquals("a gate taken now writes", 1, PairingGate(prefs).write { 1 })
    }
}
