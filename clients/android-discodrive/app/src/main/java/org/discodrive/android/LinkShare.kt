package org.discodrive.android

import android.content.SharedPreferences
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/**
 * "Share → DiscoDrive": the logic of the share target, without Android UI so it is unit
 * tested. The share screen asks the server once, briefly; what it shows depends only on
 * the answer, and links that could not get through wait in [LinkOutbox].
 */
object LinkShare {
    enum class Outcome { SAVED, QUEUED, SIGN_IN, REJECTED }

    /** How long the share screen waits for the server before it queues the link. */
    const val ANSWER_TIMEOUT_MS = 1500L

    private val urlPattern = Regex("""https?://[^\s<>"'«»]+""", RegexOption.IGNORE_CASE)

    /** The first http(s) link in shared text, without trailing punctuation. */
    fun firstWebUrl(text: String): String? {
        val m = urlPattern.find(text) ?: return null
        val url = m.value.trimEnd('.', ',', '!', '?', ')', ']', ';', ':')
        val host = url.substringAfter("://").substringBefore('/').substringBefore('?')
        return url.takeIf { host.isNotEmpty() }
    }

    /** null status: the request failed or did not answer in time. */
    fun outcome(status: Int?): Outcome = when (status) {
        null -> Outcome.QUEUED
        in 200..299 -> Outcome.SAVED
        401, 403 -> Outcome.SIGN_IN
        400, 413, 422 -> Outcome.REJECTED
        else -> Outcome.QUEUED // 429, 5xx, a proxy's error page: worth another try
    }

    /** Shared text minus the link itself; null when nothing else is left. */
    fun title(text: String?, url: String): String? {
        val t = text?.replace(url, "")?.let { s -> firstWebUrl(s)?.let { s.replace(it, "") } ?: s }?.trim()
        return t?.takeIf { it.isNotEmpty() }?.take(300)
    }

    /**
     * Sends what waits in the outbox, oldest first. Links the server took or will never
     * take leave it; the first one that cannot get through stops the flush.
     */
    fun flush(outbox: LinkOutbox, send: (url: String, title: String?, addedMs: Long) -> Int?) {
        for (e in outbox.pending()) {
            val status = runCatching { send(e.url, e.title, e.addedMs) }.getOrNull()
            when (outcome(status)) {
                Outcome.QUEUED -> return
                else -> outbox.remove(e.id)
            }
        }
    }
}

/** Links waiting to be sent, kept in the app's preferences. Only URLs and titles. */
class LinkOutbox(private val sp: SharedPreferences) {
    /** addedMs: when the link was shared, sent along so a resent copy cannot un-archive it. */
    data class Entry(val id: String, val url: String, val title: String?, val addedMs: Long)

    companion object {
        const val CAPACITY = 200
        private const val KEY = "link_outbox"
        // One lock for every instance: the share screen and the app each make their own.
        private val lock = Any()
    }

    /** Returns the entry's id, for taking it out again if the first request lands after all. */
    fun enqueue(url: String, title: String?): String = synchronized(lock) {
        val list = read().toMutableList()
        val id = UUID.randomUUID().toString()
        list += Entry(id, url, title, System.currentTimeMillis())
        while (list.size > CAPACITY) list.removeAt(0)
        save(list)
        id
    }

    fun remove(id: String) = synchronized(lock) { save(read().filterNot { it.id == id }) }

    fun pending(): List<Entry> = synchronized(lock) { read() }

    private fun read(): List<Entry> {
        val raw = sp.getString(KEY, null) ?: return emptyList()
        return runCatching {
            val arr = JSONArray(raw)
            (0 until arr.length()).map { i ->
                val o = arr.getJSONObject(i)
                Entry(o.getString("id"), o.getString("url"), if (o.has("title")) o.getString("title") else null, o.optLong("added", 0L))
            }
        }.getOrDefault(emptyList())
    }

    private fun save(list: List<Entry>) {
        val arr = JSONArray()
        list.forEach { e -> arr.put(JSONObject().put("id", e.id).put("url", e.url).put("added", e.addedMs).apply { e.title?.let { put("title", it) } }) }
        sp.edit().putString(KEY, arr.toString()).commit()
    }
}
