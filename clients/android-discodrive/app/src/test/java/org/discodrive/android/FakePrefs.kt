package org.discodrive.android

import android.content.SharedPreferences

/** In-memory SharedPreferences: android.jar's classes are stubs on the JVM, its interfaces are not. */
class FakePrefs : SharedPreferences {
    private val map = HashMap<String, Any?>()

    override fun getAll(): Map<String, *> = HashMap(map)
    override fun getString(key: String, defValue: String?): String? = map[key] as String? ?: defValue
    @Suppress("UNCHECKED_CAST")
    override fun getStringSet(key: String, defValues: Set<String>?): Set<String>? = map[key] as Set<String>? ?: defValues
    override fun getInt(key: String, defValue: Int): Int = map[key] as Int? ?: defValue
    override fun getLong(key: String, defValue: Long): Long = map[key] as Long? ?: defValue
    override fun getFloat(key: String, defValue: Float): Float = map[key] as Float? ?: defValue
    override fun getBoolean(key: String, defValue: Boolean): Boolean = map[key] as Boolean? ?: defValue
    override fun contains(key: String): Boolean = map.containsKey(key)
    override fun registerOnSharedPreferenceChangeListener(l: SharedPreferences.OnSharedPreferenceChangeListener) {}
    override fun unregisterOnSharedPreferenceChangeListener(l: SharedPreferences.OnSharedPreferenceChangeListener) {}

    override fun edit(): SharedPreferences.Editor = object : SharedPreferences.Editor {
        private val puts = HashMap<String, Any?>()
        private val removes = HashSet<String>()
        private var clear = false
        private fun self(change: () -> Unit): SharedPreferences.Editor { change(); return this }
        override fun putString(key: String, value: String?) = self { puts[key] = value }
        override fun putStringSet(key: String, values: Set<String>?) = self { puts[key] = values }
        override fun putInt(key: String, value: Int) = self { puts[key] = value }
        override fun putLong(key: String, value: Long) = self { puts[key] = value }
        override fun putFloat(key: String, value: Float) = self { puts[key] = value }
        override fun putBoolean(key: String, value: Boolean) = self { puts[key] = value }
        override fun remove(key: String) = self { removes += key }
        override fun clear() = self { clear = true }
        override fun commit(): Boolean {
            if (clear) map.clear()
            removes.forEach { map.remove(it) }
            map.putAll(puts)
            return true
        }
        override fun apply() { commit() }
    }
}
