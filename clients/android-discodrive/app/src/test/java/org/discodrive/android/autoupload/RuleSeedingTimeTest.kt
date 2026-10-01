package org.discodrive.android.autoupload

import org.discodrive.android.FakePrefs
import org.discodrive.android.Prefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class RuleSeedingTimeTest {
    @Test
    fun photoCreatedDuringSeedMustRemainUploadable() {
        val prefs = Prefs(FakePrefs()).also { it.saveServer("https://example.com", "token", "") }
        val folder = java.nio.file.Files.createTempDirectory("seed-test").toFile()
        try {
            val cutoff = System.currentTimeMillis() - 10_000
            val old = File(folder, "old.jpg").also {
                it.writeText("archive")
                assertTrue(it.setLastModified(cutoff - 10_000))
            }
            val rule = Rule.of(folder.path, listOf("Photos")).copy(createdAt = cutoff)
            val skipped = mutableListOf<File>()
            assertTrue(RuleSeeding.add(prefs, rule, scan = {
                // Camera writes before a long recursive scan reaches this directory.
                File(folder, "new.jpg").writeText("new photo")
                SourceScanner.scan(folder, true, true, Long.MAX_VALUE)
            }, record = { skipped += it }))
            assertEquals("Only the archive may be marked preexisting", listOf(old), skipped)
            assertTrue(prefs.rules.single().seeded)
        } finally {
            folder.deleteRecursively()
        }
    }
}
