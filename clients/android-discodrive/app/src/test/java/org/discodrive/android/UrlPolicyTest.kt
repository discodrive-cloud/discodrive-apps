package org.discodrive.android

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UrlPolicyTest {

    // The device token travels with every request; over plain http anyone on the path reads it.
    @Test
    fun `server must be https`() {
        assertTrue(UrlPolicy.serverUrlAllowed("https://drive.example.com"))
        assertTrue(UrlPolicy.serverUrlAllowed("https://drive.example.com:8443/"))
        assertTrue(UrlPolicy.serverUrlAllowed("HTTPS://Drive.Example.com"))
        assertFalse(UrlPolicy.serverUrlAllowed("http://drive.example.com"))
        assertFalse(UrlPolicy.serverUrlAllowed("http://192.168.1.10:8080"))
        assertFalse(UrlPolicy.serverUrlAllowed("ftp://drive.example.com"))
        assertFalse(UrlPolicy.serverUrlAllowed("drive.example.com"))
        assertFalse(UrlPolicy.serverUrlAllowed("https://"))
        assertFalse(UrlPolicy.serverUrlAllowed(""))
        assertFalse(UrlPolicy.serverUrlAllowed("not a url at all"))
    }

    @Test
    fun `plain http is allowed only for a server on this device`() {
        assertTrue(UrlPolicy.serverUrlAllowed("http://localhost:8080"))
        assertTrue(UrlPolicy.serverUrlAllowed("http://127.0.0.1:8080"))
        assertFalse(UrlPolicy.serverUrlAllowed("http://localhost.evil.com"))
        assertFalse(UrlPolicy.serverUrlAllowed("http://127.0.0.2"))
    }

    // The approval page must be the paired server's own page, opened in a browser — not a
    // tel:, sms:, market: or intent: link handed to whichever app claims that scheme.
    @Test
    fun `verification page on the server's host is opened`() {
        val server = "https://drive.example.com"
        assertTrue(UrlPolicy.verificationUrlAllowed("https://drive.example.com/pair?code=AB12", server))
        assertTrue(UrlPolicy.verificationUrlAllowed("https://DRIVE.example.com/pair", server))
    }

    @Test
    fun `verification page anywhere else is refused`() {
        val server = "https://drive.example.com"
        assertFalse(UrlPolicy.verificationUrlAllowed("https://evil.example.com/pair", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("https://drive.example.com.evil.com/pair", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("http://drive.example.com/pair", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("tel:+15551234567", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("sms:+15551234567", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("market://details?id=x", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("intent://drive.example.com#Intent;scheme=https;end", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("javascript:alert(1)", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("::::", server))
    }

    @Test
    fun `local http server may send an http verification page on the same host`() {
        val server = "http://localhost:8080"
        assertTrue(UrlPolicy.verificationUrlAllowed("http://localhost:8080/pair", server))
        assertTrue(UrlPolicy.verificationUrlAllowed("https://localhost/pair", server))
        assertFalse(UrlPolicy.verificationUrlAllowed("http://example.com/pair", server))
        // An https server never downgrades its approval page to http.
        assertFalse(UrlPolicy.verificationUrlAllowed("http://localhost/pair", "https://localhost"))
    }
}
