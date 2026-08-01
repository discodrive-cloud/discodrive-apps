package org.discodrive.android.autoupload

import android.app.Application
import android.os.Environment
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.discodrive.android.Prefs
import org.discodrive.android.R
import java.io.File

data class AutoUploadState(
    val enabled: Boolean = false,
    val rules: List<Rule> = emptyList(),
    val wifiOnly: Boolean = true,
    val chargingOnly: Boolean = false,
    val requireBattery: Boolean = true,
    val pauseOnRoaming: Boolean = true,
    val sent: Int = 0,
    val skipped: Int = 0,
    val deferred: Int = 0,
    /** Why nothing is happening right now, already translated; null when nothing blocks. */
    val blocked: String? = null,
    /** Progress line while a pass runs in-process; null when idle. */
    val running: String? = null,
)

class AutoUploadViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = Prefs(app)
    private val observers = FolderObservers(app)

    private val _state = MutableStateFlow(AutoUploadState())
    val state: StateFlow<AutoUploadState> = _state.asStateFlow()

    init { reload() }

    fun reload() {
        val ctx = getApplication<Application>()
        viewModelScope.launch {
            val counts = withContext(Dispatchers.IO) {
                val j = UploadJournal(ctx)
                try { j.counts() } finally { j.close() }
            }
            _state.value = _state.value.copy(
                enabled = prefs.autoUpload,
                rules = prefs.rules,
                wifiOnly = prefs.wifiOnly,
                chargingOnly = prefs.whileChargingOnly,
                requireBattery = prefs.requireBattery,
                pauseOnRoaming = prefs.pauseOnRoaming,
                sent = counts.sent, skipped = counts.skipped, deferred = counts.deferred,
                blocked = blockedText(),
            )
        }
    }

    /** Turning it on proposes the camera folder and starts watching; off stops everything. */
    fun setEnabled(on: Boolean) {
        val ctx = getApplication<Application>()
        prefs.autoUpload = on
        if (on) {
            if (prefs.rules.isEmpty()) prefs.addRule(AutoUploadRunner.defaultRule())
            AutoUploadWorker.schedule(ctx, prefs.wifiOnly)
            observers.start()
        } else {
            AutoUploadWorker.cancel(ctx)
            observers.stop()
        }
        reload()
    }

    fun addFolder(path: String) {
        val dir = File(path)
        if (!dir.isDirectory) return
        prefs.addRule(
            Rule.of(
                dir.path,
                AutoUploadRunner.defaultDestFor(dir),
                mediaOnly = AutoUploadRunner.defaultMediaOnlyFor(dir),
            )
        )
        if (prefs.autoUpload) observers.start() // watch the new folder too
        reload()
    }

    /**
     * Removing a folder stops future uploads from it. What is already on the server stays,
     * and so does the journal: re-adding the folder must not re-upload its whole history.
     */
    fun removeFolder(path: String) {
        prefs.removeRule(path)
        if (prefs.autoUpload) observers.start()
        reload()
    }

    /**
     * Turning subfolders on means "send what is in them" — the files are new to the journal,
     * so the next pass uploads them. That is what the switch reads like, and re-seeding them
     * as pre-existing would make it do nothing at all.
     */
    fun setSubfolders(path: String, on: Boolean) =
        updateRule(path) { it.copy(includeSubfolders = on) }

    private fun updateRule(path: String, f: (Rule) -> Rule) {
        val target = Rule.normalize(path)
        prefs.rules = prefs.rules.map { if (it.sourcePath == target) f(it) else it }
        reload()
    }

    fun setWifiOnly(on: Boolean) {
        prefs.wifiOnly = on
        if (prefs.autoUpload) AutoUploadWorker.schedule(getApplication(), on)
        reload()
    }

    fun setChargingOnly(on: Boolean) { prefs.whileChargingOnly = on; reload() }
    fun setRequireBattery(on: Boolean) { prefs.requireBattery = on; reload() }
    fun setPauseOnRoaming(on: Boolean) { prefs.pauseOnRoaming = on; reload() }

    fun uploadNow() {
        AutoUploadWorker.runNow(getApplication(), prefs.wifiOnly)
        _state.value = _state.value.copy(running = null, blocked = blockedText())
        // The pass reports through its notification; refresh the counters shortly after.
        viewModelScope.launch {
            kotlinx.coroutines.delay(3000)
            reload()
        }
    }

    fun log(): List<JournalEntry> {
        val ctx = getApplication<Application>()
        val j = UploadJournal(ctx)
        return try { j.recent(200) } finally { j.close() }
    }

    /** Where the folder picker opens: the shared storage root, which is what users browse. */
    fun pickerStart(): File = Environment.getExternalStorageDirectory()

    private fun blockedText(): String? {
        if (!prefs.autoUpload) return null
        val ctx = getApplication<Application>()
        return when (Conditions.check(ctx, prefs)) {
            Block.NONE -> null
            Block.NEEDS_WIFI -> ctx.getString(R.string.au_blocked_wifi)
            Block.ROAMING -> ctx.getString(R.string.au_blocked_roaming)
            Block.NOT_CHARGING -> ctx.getString(R.string.au_blocked_charging)
            Block.LOW_BATTERY -> ctx.getString(R.string.au_blocked_battery)
            Block.NO_NETWORK -> ctx.getString(R.string.au_blocked_network)
        }
    }

    override fun onCleared() {
        observers.stop()
        super.onCleared()
    }
}
