package org.discodrive.android

import mobile.Browser
import mobile.Mobile
import mobile.Pairing
import mobile.Vault

// Wrapper over the gomobile API (package `mobile`). All calls throw and block — use Dispatchers.IO.
object Core {
    fun pairBegin(server: String, name: String, kind: String, insecure: Boolean): Pairing =
        Mobile.pairBegin(server, name, kind, insecure)

    fun pairAwait(server: String, deviceCode: String, intervalSec: Long, insecure: Boolean): String =
        Mobile.pairAwait(server, deviceCode, intervalSec, insecure)

    fun newBrowser(server: String, token: String, rootDir: String, indexDBPath: String, insecure: Boolean): Browser =
        Mobile.newBrowser(server, token, rootDir, indexDBPath, insecure)

    fun openVault(server: String, token: String, vaultRoot: String, password: String,
                  indexDBPath: String, tmpDir: String, insecure: Boolean): Vault =
        Mobile.openVault(server, token, vaultRoot, password, indexDBPath, tmpDir, insecure)

    // --- auto-upload ---

    /** "absent" | "same" | "different" — see NameResolver. Reads the local index only. */
    fun existsWithHash(b: Browser, parentNodeID: String, name: String, sha: String): String =
        b.existsWithHash(parentNodeID, name, sha)

    /** Node id of the destination folder, created on first use. */
    fun ensureFolder(b: Browser, parentNodeID: String, name: String): String =
        b.ensureFolder(parentNodeID, name)

    /**
     * Resumable chunked upload under an explicit name. Does NOT refresh the index — call
     * [Browser.refresh] once after a batch.
     *
     * Throws on failure; a size mismatch (the file changed while it was being sent) carries
     * [SIZE_MISMATCH] in its message, which is the one failure worth handling differently.
     */
    fun uploadAs(b: Browser, localPath: String, parentNodeID: String, name: String) =
        b.uploadAs(localPath, parentNodeID, name)

    /**
     * Marker text of mobile.ErrUploadSizeMismatch. gomobile flattens Go errors to plain
     * exceptions, so the message is all that survives the boundary.
     */
    const val SIZE_MISMATCH = "file changed during upload"
}
