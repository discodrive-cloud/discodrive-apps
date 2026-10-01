package org.discodrive.android

import mobile.Browser
import mobile.Client
import mobile.Mobile
import mobile.Pairing
import mobile.Vault

// Wrapper over the gomobile API (package `mobile`). All calls throw and block — use Dispatchers.IO.
object Core {
    /**
     * The server's leaf certificate, read without trusting it, for the trust dialog. A
     * `serverPin` below is the fingerprint the user accepted there ("" = system trust only).
     */
    fun fetchCertificate(server: String): CertDetails = Mobile.fetchCertificate(server).let {
        CertDetails(it.host, it.fingerprint, it.subject, it.issuer, it.notAfter, it.selfSigned, it.trusted)
    }

    fun pairBegin(server: String, name: String, kind: String, serverPin: String): Pairing =
        Mobile.pairBegin(server, name, kind, serverPin)

    fun pairAwait(server: String, deviceCode: String, intervalSec: Long, serverPin: String): String =
        Mobile.pairAwait(server, deviceCode, intervalSec, serverPin)

    /** Ends this device on the server so its token stops working. */
    fun revokeDevice(server: String, token: String, serverPin: String) =
        Mobile.revokeDevice(server, token, serverPin)

    fun newBrowser(server: String, token: String, rootDir: String, indexDBPath: String, serverPin: String): Browser =
        Mobile.newBrowser(server, token, rootDir, indexDBPath, serverPin)

    /** The folder-sync engine (see sync/SyncHolder). */
    fun newSyncClient(server: String, token: String, syncDir: String, dbPath: String, serverPin: String): Client =
        Mobile.new_(server, token, syncDir, dbPath, serverPin)

    fun openVault(server: String, token: String, vaultRoot: String, password: String,
                  indexDBPath: String, tmpDir: String, serverPin: String): Vault =
        Mobile.openVault(server, token, vaultRoot, password, indexDBPath, tmpDir, serverPin)

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
