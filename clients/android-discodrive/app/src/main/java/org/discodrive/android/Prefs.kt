package org.discodrive.android

import android.content.Context
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import org.discodrive.android.autoupload.Rule

class Prefs(context: Context) {
    private val sp = EncryptedSharedPreferences.create(
        context, "fastsync",
        MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
    )

    var serverURL: String
        get() = sp.getString("serverURL", "") ?: ""
        set(v) { sp.edit().putString("serverURL", v).apply() }
    var deviceToken: String?
        get() = sp.getString("deviceToken", null)
        set(v) { sp.edit().putString("deviceToken", v).apply() }
    var insecure: Boolean
        get() = sp.getBoolean("insecure", false)
        set(v) { sp.edit().putBoolean("insecure", v).apply() }

    /**
     * Stores everything a pairing produced, in one synchronous write. Call it off the main
     * thread.
     *
     * The three go together — a token without a server URL does not count as paired — and
     * apply() only schedules the write, so a process killed right after pairing (swiped away
     * while the browser still had focus) could lose part of it and come back unpaired.
     */
    fun saveServer(url: String, token: String, insecureTLS: Boolean) {
        sp.edit()
            .putString("serverURL", url)
            .putString("deviceToken", token)
            .putBoolean("insecure", insecureTLS)
            .commit()
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

    /** Adds a folder if it is not already covered; returns whether it was added. */
    fun addRule(rule: Rule): Boolean {
        val current = rules
        val path = Rule.normalize(rule.sourcePath)
        if (current.any { it.sourcePath == path }) return false
        rules = current + rule
        return true
    }

    fun removeRule(sourcePath: String) {
        val path = Rule.normalize(sourcePath)
        rules = rules.filterNot { it.sourcePath == path }
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

    fun clear() { sp.edit().clear().apply() }
}
