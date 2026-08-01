package org.discodrive.android

import android.content.Context
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

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
     * Set once the source folder has been recorded as pre-existing. Until then a pass would
     * mistake the whole camera archive for new files and start uploading it.
     */
    var autoUploadSeeded: Boolean
        get() = sp.getBoolean("autoUploadSeeded", false)
        set(v) { sp.edit().putBoolean("autoUploadSeeded", v).apply() }

    /** Node id of the destination folder, cached so every pass does not re-create it. */
    var autoUploadDestID: String?
        get() = sp.getString("autoUploadDestID", null)
        set(v) { sp.edit().putString("autoUploadDestID", v).apply() }

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
