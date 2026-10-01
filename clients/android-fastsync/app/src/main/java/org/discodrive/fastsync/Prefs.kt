package org.discodrive.fastsync

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.io.File

class Prefs internal constructor(private val sp: SharedPreferences) {
    constructor(context: Context) : this(open(context))

    private companion object {
        const val FILE = "fastsync"

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
                val dir = File(context.applicationInfo.dataDir, "shared_prefs")
                runCatching { File(dir, "$FILE.xml").copyTo(File(dir, "$FILE.undecryptable-${System.currentTimeMillis()}.bak")) }
                context.deleteSharedPreferences(FILE)
                // The sync index belongs to the pairing just lost; kept, the next pairing's
                // first pass would read its files as locally deleted (see ClientHolder.wipe).
                val db = File(context.filesDir, "state.db")
                listOf(db, File(db.path + "-wal"), File(db.path + "-shm")).forEach { it.delete() }
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
        sp.edit()
            .putString("serverURL", url)
            .putString("deviceToken", token)
            .putString("server_pin", serverPin)
            // Left by versions that could switch certificate checks off; never read.
            .remove("insecure")
            .commit()
    }

    /**
     * A pairing that has been started but not yet approved.
     *
     * Approving happens in a browser — often on another device entirely — so the app spends
     * that time in the background, where it can be killed outright. The device code lived only
     * in the coroutine that was waiting, so a kill lost a pairing the server had already
     * approved: the app came back to an untouched pairing screen and starting over produced
     * the same result.
     */
    var pendingPairing: PendingPairing?
        get() = PendingPairing.fromJson(sp.getString("pendingPairing", null))
        set(v) {
            val e = sp.edit()
            if (v == null) e.remove("pendingPairing") else e.putString("pendingPairing", v.toJson())
            e.commit()
        }

    fun clear() { sp.edit().clear().apply() }
}
