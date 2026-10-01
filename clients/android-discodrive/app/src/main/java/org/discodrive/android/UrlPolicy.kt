package org.discodrive.android

import java.net.URI
import java.util.Locale

/**
 * Which URLs the app is willing to talk to or hand to another app.
 *
 * Pure (java.net.URI, not android.net.Uri) so it can be unit-tested on the JVM.
 */
object UrlPolicy {
    private val LOCAL_HOSTS = setOf("localhost", "127.0.0.1")

    private fun parse(url: String): URI? = runCatching { URI(url.trim()) }.getOrNull()

    private fun URI.schemeLc(): String? = scheme?.lowercase(Locale.US)
    private fun URI.hostLc(): String? = host?.lowercase(Locale.US)?.takeIf { it.isNotEmpty() }

    /**
     * A server the device may pair with. The device token rides on every request, and the
     * Go client does not honour Android's cleartext policy, so plain http is refused — except
     * for a server on this very device, which is how development runs.
     */
    fun serverUrlAllowed(url: String): Boolean {
        val u = parse(url) ?: return false
        val host = u.hostLc() ?: return false
        return when (u.schemeLc()) {
            "https" -> true
            "http" -> host in LOCAL_HOSTS
            else -> false
        }
    }

    /**
     * Whether the approval page the server sent back may be opened with ACTION_VIEW.
     *
     * The URL comes from the server, and ACTION_VIEW goes to whichever app claims its scheme:
     * tel:, sms:, market: or an intent: link would do something the user never asked for. So
     * only a web page on the paired server's own host passes — https, or http when the server
     * itself is a local http one.
     */
    fun verificationUrlAllowed(url: String, serverUrl: String): Boolean {
        val server = parse(serverUrl) ?: return false
        val serverHost = server.hostLc() ?: return false
        val u = parse(url) ?: return false
        if (u.hostLc() != serverHost) return false
        return when (u.schemeLc()) {
            "https" -> true
            "http" -> server.schemeLc() == "http" && serverHost in LOCAL_HOSTS
            else -> false
        }
    }
}
