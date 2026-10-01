package org.discodrive.android

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class PendingPairingTest {
    @Test
    fun `round trip keeps the pin`() {
        val p = PendingPairing("https://nas.local", "dev", "ABCD-1234", 3, "AB:CD")
        assertEquals(p, PendingPairing.fromJson(p.toJson()))
        assertEquals("AB:CD", JSONObject(p.toJson()).getString("pin"))
    }

    @Test
    fun `no insecure key is written`() {
        val p = PendingPairing("https://nas.local", "dev", "ABCD-1234", 3, "")
        assertFalse(JSONObject(p.toJson()).has("insecure"))
    }

    @Test
    fun `an older pairing with insecure set resumes strict`() {
        val old = """{"server":"https://nas.local","deviceCode":"dev","userCode":"U","intervalSeconds":5,"insecure":true}"""
        assertEquals(PendingPairing("https://nas.local", "dev", "U", 5, ""), PendingPairing.fromJson(old))
    }
}
