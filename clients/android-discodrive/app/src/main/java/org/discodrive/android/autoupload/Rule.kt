package org.discodrive.android.autoupload

import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * One "this folder goes to that place on the server" rule.
 *
 * The user picks folders; each pick becomes a rule. The camera folder is just the rule the
 * app proposes first — nothing in the pass treats it specially.
 *
 * @param sourcePath absolute path of the folder on the phone, normalised (see [normalize])
 * @param destSegments folder names on the server, top-down, e.g. ["DeviceUploads", "Pixel 8"]
 * @param seeded whether the folder's existing contents have been recorded as pre-existing;
 *   until that has happened, switching a rule on would upload the whole archive
 * @param destID cached node id of the destination, so a pass does not re-resolve it
 */
data class Rule(
    val sourcePath: String,
    val destSegments: List<String>,
    val mediaOnly: Boolean = true,
    val includeSubfolders: Boolean = false,
    val enabled: Boolean = true,
    val seeded: Boolean = false,
    val destID: String? = null,
) {
    val source: File get() = File(sourcePath)

    /** Shown in the UI; also what the user recognises the rule by. */
    val sourceLabel: String get() = source.name.ifEmpty { sourcePath }
    val destLabel: String get() = "/" + destSegments.joinToString("/")

    fun toJson(): JSONObject = JSONObject().apply {
        put("sourcePath", sourcePath)
        put("dest", JSONArray(destSegments))
        put("mediaOnly", mediaOnly)
        put("includeSubfolders", includeSubfolders)
        put("enabled", enabled)
        put("seeded", seeded)
        destID?.let { put("destID", it) }
    }

    companion object {
        /**
         * `/sdcard/DCIM` and `/storage/emulated/0/DCIM` are the same folder, and both the
         * rules and the journal key off the path — so two spellings mean the same photo can
         * be recorded, and uploaded, twice. Every stored path goes through here.
         */
        fun normalize(path: String): String =
            runCatching { File(path).canonicalPath }.getOrDefault(File(path).absolutePath)

        /** Builds a rule with its source path normalised. */
        fun of(
            sourcePath: String,
            destSegments: List<String>,
            mediaOnly: Boolean = true,
            includeSubfolders: Boolean = false,
        ) = Rule(
            sourcePath = normalize(sourcePath),
            destSegments = destSegments,
            mediaOnly = mediaOnly,
            includeSubfolders = includeSubfolders,
        )

        fun fromJson(o: JSONObject): Rule {
            val dest = o.optJSONArray("dest") ?: JSONArray()
            return Rule(
                sourcePath = normalize(o.getString("sourcePath")),
                destSegments = (0 until dest.length()).map { dest.getString(it) },
                mediaOnly = o.optBoolean("mediaOnly", true),
                includeSubfolders = o.optBoolean("includeSubfolders", false),
                enabled = o.optBoolean("enabled", true),
                seeded = o.optBoolean("seeded", false),
                destID = if (o.has("destID")) o.getString("destID") else null,
            )
        }

        fun listToJson(rules: List<Rule>): String =
            JSONArray().apply { rules.forEach { put(it.toJson()) } }.toString()

        /**
         * Unparseable storage yields no rules rather than throwing: a corrupt preference must
         * not make the app unusable, and the user can re-add the folders.
         */
        fun listFromJson(s: String?): List<Rule> {
            if (s.isNullOrBlank()) return emptyList()
            return try {
                val arr = JSONArray(s)
                (0 until arr.length()).map { fromJson(arr.getJSONObject(it)) }
            } catch (e: Exception) {
                emptyList()
            }
        }
    }
}
