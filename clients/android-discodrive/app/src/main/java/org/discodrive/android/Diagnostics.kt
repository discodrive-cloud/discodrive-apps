package org.discodrive.android

import android.content.Context
import java.io.File
import org.json.JSONObject

/** Optional bounded file logging. Disabled logging does not create directories or files. */
object Diagnostics {
    fun file(context: Context) = File(context.filesDir, "diagnostics/sync.log")
    @Synchronized
    fun record(context: Context, activity: String, message: String? = null) {
        if (!Prefs(context).loggingEnabled) return
        runCatching {
            val target = file(context)
            target.parentFile?.mkdirs()
            if (target.length() >= 2 * 1024 * 1024) {
                val previous = File(target.parentFile, "sync.previous.log")
                if (previous.exists() && !previous.delete()) return
                if (!target.renameTo(previous)) return
            }
            val safeMessage = message?.replace(Regex("https?://[^\\s]+"), "[server]")
            val row = JSONObject().put("time", java.time.Instant.now().toString())
                .put("activity", JSONObject(activity)).put("error", safeMessage ?: JSONObject.NULL)
            target.appendText(row.toString() + "\n")
        }
    }
}
