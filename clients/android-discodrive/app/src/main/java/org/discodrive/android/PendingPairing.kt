package org.discodrive.android

import org.json.JSONObject

/**
 * A pairing waiting for the user to approve it in a browser.
 *
 * Everything needed to resume the wait after the app has been killed: the server it was
 * started against and the certificate pin it used, the device code to poll with, and the
 * user code to keep showing while it is outstanding.
 */
data class PendingPairing(
    val server: String,
    val deviceCode: String,
    val userCode: String,
    val intervalSeconds: Long,
    /** Certificate fingerprint the user trusted for this server; "" = system trust only. */
    val pin: String,
) {
    fun toJson(): String = JSONObject()
        .put("server", server)
        .put("deviceCode", deviceCode)
        .put("userCode", userCode)
        .put("intervalSeconds", intervalSeconds)
        .put("pin", pin)
        .toString()

    companion object {
        fun fromJson(s: String?): PendingPairing? {
            if (s.isNullOrEmpty()) return null
            return runCatching {
                val o = JSONObject(s)
                PendingPairing(
                    server = o.getString("server"),
                    deviceCode = o.getString("deviceCode"),
                    userCode = o.optString("userCode"),
                    intervalSeconds = o.optLong("intervalSeconds", 2),
                    // An older pairing's "insecure" flag is ignored: it resumes strict.
                    pin = o.optString("pin", ""),
                )
            }.getOrNull()
        }
    }
}
