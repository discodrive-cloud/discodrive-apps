package org.discodrive.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneOffset
import java.util.Locale

class CertTrustTest {
    private val fp = (0 until 32).joinToString(":") { "%02X".format(it * 7 % 256) }

    private fun cert(trusted: Boolean, fingerprint: String = fp) = CertDetails(
        host = "nas.local:8443", fingerprint = fingerprint, subject = "nas.local",
        issuer = "nas.local", notAfter = "2027-03-01T12:00:00Z", selfSigned = true, trusted = trusted,
    )

    @Test
    fun `fingerprint is shown as four rows of eight bytes`() {
        val lines = CertTrust.fingerprintLines(fp).split("\n")
        assertEquals(4, lines.size)
        lines.forEach { assertEquals(8, it.split(":").size) }
        assertEquals(fp, lines.joinToString(":"))
    }

    @Test
    fun `fingerprint display normalises case and separators`() {
        val raw = fp.replace(":", "").lowercase()
        assertEquals(CertTrust.fingerprintLines(fp), CertTrust.fingerprintLines(raw))
    }

    @Test
    fun `malformed fingerprint is shown as it came`() {
        assertEquals("not-a-hash", CertTrust.fingerprintLines("not-a-hash"))
        assertEquals("", CertTrust.fingerprintLines(""))
    }

    @Test
    fun `certificate change is recognised anywhere in the message`() {
        assertTrue(CertTrust.isCertificateChanged("Get \"https://x/api\": server certificate changed: expected AA, got BB"))
        assertTrue(CertTrust.isCertificateChanged("server certificate changed"))
        assertFalse(CertTrust.isCertificateChanged("x509: certificate signed by unknown authority"))
        assertFalse(CertTrust.isCertificateChanged(null))
    }

    @Test
    fun `a changed certificate gets the explanation, other errors pass through`() {
        val msg = "server certificate changed: expected AA, got BB"
        assertEquals("Explained\n\n$msg", CertTrust.describe(msg, "Explained"))
        assertEquals("timeout", CertTrust.describe("timeout", "Explained"))
        assertNull(CertTrust.describe(null, "Explained"))
    }

    @Test
    fun `expiry is a date in the given locale and zone`() {
        assertEquals("Mar 1, 2027", CertTrust.expiryDate("2027-03-01T12:00:00Z", Locale.US, ZoneOffset.UTC))
        assertEquals("garbage", CertTrust.expiryDate("garbage", Locale.US, ZoneOffset.UTC))
    }

    @Test
    fun `only an untrusted certificate with a fingerprint is offered`() {
        assertEquals(cert(false), CertTrust.offerFor(cert(false)))
        assertNull(CertTrust.offerFor(cert(true)))
        assertNull(CertTrust.offerFor(cert(false, fingerprint = "")))
        assertNull(CertTrust.offerFor(null))
    }
}
