package org.discodrive.fastsync

import mobile.Client
import mobile.Mobile
import mobile.Pairing

// Wrapper over the gomobile-generated Kfmobile API (package `mobile`). All methods throw
// Exception. They block — call from Dispatchers.IO.
object SyncCore {
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

    fun newClient(server: String, token: String, syncDir: String, dbPath: String, serverPin: String): Client =
        Mobile.new_(server, token, syncDir, dbPath, serverPin)

    /**
     * Marker text of a pass stopped by the mass-deletion check. gomobile flattens Go errors to
     * plain exceptions, so the message is all that survives the boundary.
     */
    const val BULK_DELETE_MARKER = "refusing to delete"
}
