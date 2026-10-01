package org.discodrive.fastsync

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PrefsPinTest {
    @Test
    fun `no pin until one is saved`() {
        assertEquals("", Prefs(FakePrefs()).serverPin)
    }

    @Test
    fun `the old insecure flag is ignored and dropped on the next save`() {
        val sp = FakePrefs()
        sp.edit().putBoolean("insecure", true).commit()
        val prefs = Prefs(sp)
        assertEquals("", prefs.serverPin)

        prefs.saveServer("https://nas.local", "tok", "AB:CD")
        assertFalse(sp.contains("insecure"))
        assertEquals("AB:CD", sp.getString("server_pin", null))
        assertEquals("AB:CD", prefs.serverPin)
        assertEquals("https://nas.local", prefs.serverURL)
        assertEquals("tok", prefs.deviceToken)
    }

    @Test
    fun `unpairing forgets the pin with the token`() {
        val sp = FakePrefs()
        val prefs = Prefs(sp)
        prefs.saveServer("https://nas.local", "tok", "AB:CD")
        prefs.clear()
        assertEquals("", prefs.serverPin)
        assertTrue(sp.all.isEmpty())
    }
}
