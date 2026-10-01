package org.discodrive.android.autoupload

import org.discodrive.android.Prefs
import java.io.File

/**
 * Which pairing auto-upload's stored state (the journal, the rule list) belongs to.
 *
 * Adding a folder scans it first, which can take a long while and is not cancelled by
 * leaving the screen. An unpair that runs meanwhile wipes the journal and the prefs; a scan
 * finishing after that wrote the old account's journal entries and rule back, and the next
 * pairing inherited the folder. Unpairing ends the generation under [lock]; a write begun
 * under an earlier generation finds that under the same lock and is dropped.
 */
object PairingGeneration {
    private val lock = Any()
    private var epoch = 0L

    fun current(): Long = synchronized(lock) { epoch }

    /**
     * Starts a new generation and runs [cleanup] under the lock: every write begun before it
     * is refused from here on. Run by the unpair cleanup and by every new pairing
     * (Prefs.saveServer), so a gate is safe even if one unpair path skips it.
     */
    fun <T> end(cleanup: () -> T): T = synchronized(lock) {
        epoch++
        cleanup()
    }

    /** Runs [write] only if the pairing captured as [generation] is still current; null otherwise. */
    fun <T> whileCurrent(generation: Long, write: () -> T): T? = synchronized(lock) {
        if (epoch == generation) write() else null
    }
}

/**
 * Lets writes through only for the pairing in place when the gate was made: the generation
 * has not moved on (see [PairingGeneration]) and the device is still paired, not unpairing.
 * A write runs under the generation lock, so the unpair cleanup either comes before it (and
 * the write is dropped) or waits for it to finish — never in the middle.
 *
 * The auto-upload worker takes one at the start of a pass: everything the pass writes to the
 * journal or the rule list after an unpair would belong to the old account.
 */
class PairingGate(private val prefs: Prefs, private val generation: Long = PairingGeneration.current()) {
    private fun stillPaired() = !prefs.unpairing && !prefs.deviceToken.isNullOrEmpty()

    /** Runs [block] and returns its result, or returns null without running it. */
    fun <T> write(block: () -> T): T? = PairingGeneration.whileCurrent(generation) {
        if (stillPaired()) block() else null
    }
}

object RuleSeeding {
    /**
     * Records what [rule]'s folder already holds as pre-existing (through [record]), then
     * stores the rule as seeded. The rule reaches the stored list only after its journal
     * entries exist, so no pass can ever see it unseeded. [scan] runs outside any lock; the
     * writes run only if the pairing the folder was added under is still the one in place.
     *
     * A folder that is already a rule is left alone: seeding it again would mark its
     * not-yet-sent files as pre-existing. Returns whether the rule was added.
     */
    fun add(prefs: Prefs, rule: Rule, scan: (Rule) -> List<File>, record: (List<File>) -> Unit): Boolean {
        val gate = PairingGate(prefs)
        if (gate.write { true } != true || prefs.rules.any { it.sourcePath == rule.sourcePath }) return false
        val existing = SourceScanner.preexisting(scan(rule), rule.createdAt)
        return gate.write {
            if (prefs.rules.any { it.sourcePath == rule.sourcePath }) return@write false
            record(existing)
            prefs.addRule(rule.copy(seeded = true))
        } ?: false
    }
}
