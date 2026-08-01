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
        if (current.any { it.sourcePath == rule.sourcePath }) return false
        rules = current + rule
        return true
    }

    fun removeRule(sourcePath: String) {
        rules = rules.filterNot { it.sourcePath == sourcePath }
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
