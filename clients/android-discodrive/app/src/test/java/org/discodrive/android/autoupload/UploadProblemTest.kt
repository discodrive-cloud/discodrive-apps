package org.discodrive.android.autoupload

import org.discodrive.android.FakePrefs
import org.discodrive.android.Prefs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.io.IOException
import java.net.ConnectException
import java.net.SocketTimeoutException
import java.net.UnknownHostException

class UploadProblemTest {
    // Go errors reach Kotlin as plain exceptions carrying the error text.
    private fun go(message: String) = Exception(message)

    @Test
    fun `a full disk is recognised in the core's and the JVM's wording`() {
        assertEquals(UploadProblem.DISK_FULL, UploadProblem.classify(go("write /data/user/0/x/index.db: no space left on device")))
        assertEquals(UploadProblem.DISK_FULL, UploadProblem.classify(IOException("write failed: ENOSPC (No space left on device)")))
    }

    @Test
    fun `network failures from the core and from the JVM`() {
        listOf(
            "Put \"https://fd.example/upload/1/chunk/0\": dial tcp 66.45.230.235:443: i/o timeout",
            "Get \"https://fd.example/sync/changes\": dial tcp: lookup fd.example: no such host",
            "dial tcp 10.0.0.2:443: connect: connection refused",
            "read tcp 10.0.0.3:5555->66.45.230.235:443: read: connection reset by peer",
            "dial tcp 66.45.230.235:443: connect: network is unreachable",
            "net/http: TLS handshake timeout",
            "upload chunk 3: context deadline exceeded",
            "unexpected EOF",
        ).forEach { assertEquals(it, UploadProblem.NETWORK, UploadProblem.classify(go(it))) }
        assertEquals(UploadProblem.NETWORK, UploadProblem.classify(SocketTimeoutException("timeout")))
        assertEquals(UploadProblem.NETWORK, UploadProblem.classify(UnknownHostException("fd.example")))
        assertEquals(UploadProblem.NETWORK, UploadProblem.classify(ConnectException("refused")))
    }

    @Test
    fun `server statuses, sign-in, the certificate and the rest`() {
        assertEquals(UploadProblem.SERVER, UploadProblem.classify(go("upload chunk: 502")))
        assertEquals(UploadProblem.SERVER, UploadProblem.classify(go("complete upload: 500: internal error")))
        assertEquals(UploadProblem.AUTH, UploadProblem.classify(go("list folder: 401: unauthorized")))
        assertEquals(UploadProblem.AUTH, UploadProblem.classify(go("device token exchange: 403")))
        assertEquals(UploadProblem.CERTIFICATE, UploadProblem.classify(go("tls: server certificate changed: was AA, now BB")))
        assertEquals(UploadProblem.OTHER, UploadProblem.classify(go("upload init: 409: name taken")))
        assertEquals(UploadProblem.OTHER, UploadProblem.classify(go("something odd")))
        assertEquals(UploadProblem.OTHER, UploadProblem.classify(RuntimeException()))
    }

    @Test
    fun `a single network hiccup stays quiet, repeated ones and real problems show`() {
        var s = ProblemState()
        s = s.after(UploadProblem.NETWORK)
        assertNull(s.visible)
        s = s.after(UploadProblem.NETWORK).after(UploadProblem.NETWORK)
        assertEquals(UploadProblem.NETWORK, s.visible)
        assertEquals(UploadProblem.DISK_FULL, ProblemState().after(UploadProblem.DISK_FULL).visible)
        // A different problem starts its own count.
        assertNull(s.after(UploadProblem.DISK_FULL).after(UploadProblem.NETWORK).visible)
    }

    @Test
    fun `a clean pass clears what an earlier one reported`() {
        val s = ProblemState().after(UploadProblem.SERVER)
        assertEquals(UploadProblem.SERVER, s.visible)
        assertNull(s.after(null).visible)
        assertEquals(0, s.after(null).streak)
    }

    @Test
    fun `the state survives in prefs and unknown stored values are ignored`() {
        val sp = FakePrefs()
        val prefs = Prefs(sp)
        assertEquals(ProblemState(), prefs.autoUploadProblem)
        prefs.autoUploadProblem = ProblemState(UploadProblem.DISK_FULL, 2)
        assertEquals(ProblemState(UploadProblem.DISK_FULL, 2), Prefs(sp).autoUploadProblem)
        sp.edit().putString("autoUploadProblem", "FROM_A_NEWER_BUILD").apply()
        assertEquals(ProblemState(), Prefs(sp).autoUploadProblem)
    }
}
