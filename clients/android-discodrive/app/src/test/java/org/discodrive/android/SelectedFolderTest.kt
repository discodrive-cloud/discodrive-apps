package org.discodrive.android

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test

class SelectedFolderTest {
    @Test fun navigationWhileWaitingDiscardsUnlock() = runBlocking {
        var selected = "vault-a"
        val ready = CompletableDeferred<Unit>()
        var resolved: String? = null
        val pending = async(start = CoroutineStart.UNDISPATCHED) {
            resolveSelectedFolder({ selected }) { id ->
                ready.await()
                resolved = id
                "path/$id"
            }
        }
        selected = "parent"
        ready.complete(Unit)
        assertNull(pending.await())
        assertEquals("vault-a", resolved)
    }

    @Test fun unchangedSelectionOpensRequestedVault() = runBlocking {
        assertEquals("path/vault-a", resolveSelectedFolder({ "vault-a" }) { "path/$it" })
    }
}
