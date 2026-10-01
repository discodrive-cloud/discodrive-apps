package org.discodrive.android

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import org.discodrive.android.autoupload.Rule

class Prefs internal constructor(private val sp: SharedPreferences) {
    constructor(context: Context) : this(open(context))

    private companion object {
        const val FILE = "fastsync"

        /** Serialises read-modify-write of the rule list across every Prefs instance. */
        val rulesLock = Any()

        /** The stored keyset or values fail authentication or parsing: the key is not the one that wrote them. */
        private fun undecryptable(e: Throwable): Boolean = generateSequence(e) { it.cause }.any {
            it is javax.crypto.AEADBadTagException || it.javaClass.simpleName == "InvalidProtocolBufferException"
        }

        /**
         * Opens the encrypted store. If its contents cannot be decrypted — the Keystore key
         * that wrapped them is gone, as after a restore onto another phone — the file is
         * set aside and the app starts unpaired, instead of crashing on every launch.
         *
         * Only a decryption failure does that. Failing to reach the Keystore at all (busy
         * right after boot, a vendor hiccup) is thrown as is: resetting then would throw
         * away a perfectly good pairing.
         */
        fun open(context: Context): SharedPreferences {
            val key = MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
            fun create() = EncryptedSharedPreferences.create(
                context, FILE, key,
                EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
                EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
            )
            return try {
                create()
            } catch (e: Exception) {
                if (!undecryptable(e)) throw e
                // Kept aside, not lost; deleteSharedPreferences (unlike a rename) also drops
                // the process's cached copy, so create() below really starts empty.
                val dir = java.io.File(context.applicationInfo.dataDir, "shared_prefs")
                runCatching { java.io.File(dir, "$FILE.xml").copyTo(java.io.File(dir, "$FILE.undecryptable-${System.currentTimeMillis()}.bak")) }
                context.deleteSharedPreferences(FILE)
                // What the journal says was sent went to the pairing just lost.
                org.discodrive.android.autoupload.UploadJournal.wipe(context)
                create()
            }
        }
    }

    var serverURL: String
        get() = sp.getString("serverURL", "") ?: ""
        set(v) { sp.edit().putString("serverURL", v).apply() }
    var deviceToken: String?
        get() = sp.getString("deviceToken", null)
        set(v) { sp.edit().putString("deviceToken", v).apply() }
    /**
     * SHA-256 fingerprint of the server certificate the user trusted at pairing; "" when the
     * server has a certificate the system trusts. Passed to every core call.
     */
    val serverPin: String
        get() = sp.getString("server_pin", "") ?: ""

    /**
     * Stores everything a pairing produced, in one synchronous write. Call it off the main
     * thread.
     *
     * They go together — a token without a server URL does not count as paired — and
     * apply() only schedules the write, so a process killed right after pairing (swiped away
     * while the browser still had focus) could lose part of it and come back unpaired.
     */
    fun saveServer(url: String, token: String, serverPin: String) {
        // A new pairing starts a new generation: an auto-upload write begun under an earlier
        // one is dropped even if that pairing's unpair never ended its generation.
        org.discodrive.android.autoupload.PairingGeneration.end { saveServerNow(url, token, serverPin) }
    }

    private fun saveServerNow(url: String, token: String, serverPin: String) {
        val saved = sp.edit()
            .putString("serverURL", url)
            .putString("deviceToken", token)
            .putString("server_pin", serverPin)
            // Left by versions that could switch certificate checks off; never read.
            .remove("insecure")
            .putBoolean("unpairing", false)
            .commit()
        check(saved) { "Could not save the pairing" }
    }

    /**
     * A pairing that has been started but not yet approved.
     *
     * Approving happens in a browser — often on another device entirely — so the app spends
     * that time in the background, where it can be killed outright. The device code lived only
     * in the coroutine that was waiting, so a kill lost a pairing the server had already
     * approved: the app came back to an untouched pairing screen and starting over produced
     * the same result. Kept here, the wait can be picked up again on the next launch.
     */
    var pendingPairing: PendingPairing?
        get() = PendingPairing.fromJson(sp.getString("pendingPairing", null))
        set(v) {
            val e = sp.edit()
            if (v == null) e.remove("pendingPairing") else e.putString("pendingPairing", v.toJson())
            e.commit()
        }

    var loggingEnabled: Boolean
        get() = sp.getBoolean("loggingEnabled", false)
        set(v) { sp.edit().putBoolean("loggingEnabled", v).apply() }

    var unpairing: Boolean
        get() = sp.getBoolean("unpairing", false)
        set(v) { check(sp.edit().putBoolean("unpairing", v).commit()) }

    // --- folder sync ---

    /** Whether the folder chosen on the server is mirrored on this phone (see sync/). */
    var folderSync: Boolean
        get() = sp.getBoolean("folderSync", false)
        set(v) { sp.edit().putBoolean("folderSync", v).apply() }

    // --- auto-upload ---

    /** Master switch. Off until the user turns it on; nothing is uploaded in the meantime. */
    var autoUpload: Boolean
        get() = sp.getBoolean("autoUpload", false)
        set(v) { sp.edit().putBoolean("autoUpload", v).apply() }

    /**
     * The folders the user chose to upload, with where each one goes. Empty until the
     * feature is switched on, which seeds it with the camera folder.
     */
    var rules: List<Rule>
        get() = Rule.listFromJson(sp.getString("autoUploadRules", null))
        set(v) { sp.edit().putString("autoUploadRules", Rule.listToJson(v)).apply() }

    /**
     * Replaces the rule list with [change] applied to the current one, atomically with
     * respect to every other rule update: a slow pass (seeding scans folders for seconds)
     * must not write back a list read before the user added or removed a folder.
     */
    fun updateRules(change: (List<Rule>) -> List<Rule>) = synchronized(rulesLock) {
        // apply() updates the in-memory map before returning, so the next reader under the
        // lock already sees this list.
        sp.edit().putString("autoUploadRules", Rule.listToJson(change(rules))).apply()
    }

    /** Adds a folder if it is not already covered; returns whether it was added. */
    fun addRule(rule: Rule): Boolean {
        val path = Rule.normalize(rule.sourcePath)
        var added = false
        updateRules { current ->
            if (current.any { it.sourcePath == path }) current else { added = true; current + rule }
        }
        return added
    }

    fun removeRule(sourcePath: String) {
        val path = Rule.normalize(sourcePath)
        updateRules { current -> current.filterNot { it.sourcePath == path } }
    }

    var wifiOnly: Boolean
        get() = sp.getBoolean("wifiOnly", true)
        set(v) { sp.edit().putBoolean("wifiOnly", v).apply() }
    var whileChargingOnly: Boolean
        get() = sp.getBoolean("whileChargingOnly", false)
        set(v) { sp.edit().putBoolean("whileChargingOnly", v).apply() }
    var requireBattery: Boolean
        get() = sp.getBoolean("requireBattery", true)
        set(v) { sp.edit().putBoolean("requireBattery", v).apply() }
    var pauseOnRoaming: Boolean
        get() = sp.getBoolean("pauseOnRoaming", true)
        set(v) { sp.edit().putBoolean("pauseOnRoaming", v).apply() }

    fun clear() { check(sp.edit().clear().commit()) { "Could not clear the pairing" } }
}
