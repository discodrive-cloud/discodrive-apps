package org.discodrive.android.autoupload

import org.discodrive.android.CertTrust
import java.net.ConnectException
import java.net.NoRouteToHostException
import java.net.SocketException
import java.net.SocketTimeoutException
import java.net.UnknownHostException

/**
 * What went wrong with an auto-upload, in terms a person can act on. The raw error text
 * stays in the log; the screen only ever shows one of these.
 *
 * Failures from the Go core arrive as plain exceptions carrying the error text, so most of
 * the classification reads the message.
 */
enum class UploadProblem {
    DISK_FULL, NETWORK, SERVER, AUTH, CERTIFICATE, OTHER;

    /** Failures that clear up on their own: a single one is not worth alarming anyone. */
    val isTransient: Boolean get() = this == NETWORK

    companion object {
        private val networkTypes = listOf(
            SocketTimeoutException::class.java, UnknownHostException::class.java,
            ConnectException::class.java, NoRouteToHostException::class.java, SocketException::class.java,
        )
        private val networkTexts = listOf(
            "i/o timeout", "no such host", "connection refused", "connection reset",
            "network is unreachable", "no route to host", "tls handshake timeout",
            "context deadline exceeded", "unexpected eof", "broken pipe",
        )
        // The core's StatusError reads "<operation>: <status>" or "<operation>: <status>: <body>".
        private val status = Regex(""": (\d{3})(:|$)""")

        fun classify(e: Throwable): UploadProblem {
            if (networkTypes.any { it.isInstance(e) }) return NETWORK
            val message = e.message ?: return OTHER
            val lower = message.lowercase()
            return when {
                CertTrust.isCertificateChanged(message) -> CERTIFICATE
                "no space left on device" in lower || "enospc" in lower -> DISK_FULL
                networkTexts.any { it in lower } -> NETWORK
                else -> when (status.find(message)?.groupValues?.get(1)?.toInt()) {
                    401, 403 -> AUTH
                    in 500..599 -> SERVER
                    else -> OTHER
                }
            }
        }
    }
}

/**
 * The problem the last passes reported, and how many passes in a row reported it. A clean
 * pass forgets it: what it described is no longer true.
 */
data class ProblemState(val problem: UploadProblem? = null, val streak: Int = 0) {
    fun after(pass: UploadProblem?): ProblemState = when (pass) {
        null -> ProblemState()
        problem -> copy(streak = streak + 1)
        else -> ProblemState(pass, 1)
    }

    /** What the screen should say, if anything. */
    val visible: UploadProblem?
        get() = problem?.takeIf { !it.isTransient || streak >= 3 }
}
