package org.discodrive.fastsync

import org.json.JSONObject

/**
 * A pairing waiting for the user to approve it in a browser.
 *
 * Everything needed to resume the wait after the app has been killed: the server it was
 * started against, the device code to poll with, and the user code to keep showing while it
 * is outstanding.
 */
data class PendingPairing(
    val server: String,
    val deviceCode: String,
    val userCode: String,
    val intervalSeconds: Long,
    val insecure: Boolean,
) {
    fun toJson(): String = JSONObject()
        .put("server", server)
        .put("deviceCode", deviceCode)
        .put("userCode", userCode)
        .put("intervalSeconds", intervalSeconds)
        .put("insecure", insecure)
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
                    insecure = o.optBoolean("insecure"),
                )
            }.getOrNull()
        }
    }
}
