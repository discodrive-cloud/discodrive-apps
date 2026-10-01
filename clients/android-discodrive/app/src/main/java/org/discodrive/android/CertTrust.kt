package org.discodrive.android

import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/** A server certificate as the trust dialog shows it (from mobile.Certificate). */
data class CertDetails(
    val host: String,
    val fingerprint: String,
    val subject: String,
    val issuer: String,
    /** RFC 3339. */
    val notAfter: String,
    val selfSigned: Boolean,
    val trusted: Boolean,
)

/** Pure helpers of the trust-on-first-use pairing flow; no Android or gomobile types. */
object CertTrust {
    /**
     * Text of mobile.CertificateChangedMarker, copied because touching the gomobile class
     * loads the native library. The core puts it inside a longer message, hence contains().
     */
    const val CHANGED_MARKER = "server certificate changed"

    fun isCertificateChanged(message: String?): Boolean = message?.contains(CHANGED_MARKER) == true

    /** A pin mismatch gets [explanation] in front of the core's message, which names both fingerprints. */
    fun describe(message: String?, explanation: String): String? =
        if (isCertificateChanged(message)) "$explanation\n\n$message" else message

    /** Worth asking about only when the system rejects it and there is something to pin. */
    fun offerFor(cert: CertDetails?): CertDetails? =
        cert?.takeIf { !it.trusted && it.fingerprint.isNotBlank() }

    /** Rows of eight bytes: a SHA-256 reads as four aligned lines in monospace. */
    fun fingerprintLines(fingerprint: String): String {
        val hex = fingerprint.filter { !it.isWhitespace() && it != ':' }.uppercase()
        if (hex.isEmpty() || hex.length % 2 != 0 || !hex.all { it in '0'..'9' || it in 'A'..'F' }) return fingerprint
        return hex.chunked(2).chunked(8).joinToString("\n") { it.joinToString(":") }
    }

    fun expiryDate(notAfter: String, locale: Locale = Locale.getDefault(), zone: ZoneId = ZoneId.systemDefault()): String =
        runCatching {
            OffsetDateTime.parse(notAfter).atZoneSameInstant(zone)
                .format(DateTimeFormatter.ofLocalizedDate(FormatStyle.MEDIUM).withLocale(locale))
        }.getOrDefault(notAfter)
}
