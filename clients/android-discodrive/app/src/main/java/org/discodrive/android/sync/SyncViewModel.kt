package org.discodrive.android.sync

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.work.WorkManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import org.json.JSONObject
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.discodrive.android.Prefs

data class SyncState(
    val activity: String = "{}",
    val enabled: Boolean = false,
    val working: Boolean = false,
    val changing: Boolean = false,
    val state: String = "idle",
    val lastSyncUnix: Long = 0,
    val lastError: String? = null,
    /** Where the first pass after pairing moved the folder's previous contents, if any. */
    val setAside: String? = null,
)

/** The folder-sync screen's state: the switch, and what the last pass said. */
class SyncViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private val _ui = MutableStateFlow(SyncState(enabled = prefs.folderSync))
    val ui: StateFlow<SyncState> = _ui.asStateFlow()

    val syncDir get() = SyncHolder.syncDir

    init {
        refreshStatus(null); watchSyncWork()
        viewModelScope.launch {
            while (isActive) {
                delay(1500)
                if (!prefs.folderSync) continue
                val result = withContext(Dispatchers.IO) { runCatching { SyncHolder.use(getApplication()) { Pair(it.status(), it.activityJSON()) } } }
                if (!prefs.folderSync) continue
                result.exceptionOrNull()?.let { _ui.value = _ui.value.copy(lastError = it.message) }
                val snapshot = result.getOrNull()
                snapshot?.let { (status, activity) ->
                    _ui.value = _ui.value.copy(activity = activity, state = status.state,
                        working = status.state == "syncing", lastSyncUnix = status.lastSyncUnix,
                        lastError = status.lastError.takeIf { it.isNotEmpty() },
                        setAside = status.setAside.takeIf { it.isNotEmpty() })
                }
            }
        }
    }

    fun setEnabled(on: Boolean) {
        if (_ui.value.changing) return
        prefs.folderSync = on
        _ui.value = _ui.value.copy(enabled = on)
        val app = getApplication<Application>()
        if (on) {
            SyncWorker.schedule(app)
            SyncEvents.start(app)
            syncNow()
        } else {
            SyncWorker.cancel(app)
            SyncEvents.stop(app)
            _ui.value = _ui.value.copy(working = false, changing = true, state = "idle", lastError = null)
            // The folder stays; only the engine goes. Off the main thread: closing waits.
            viewModelScope.launch {
                val failure = withContext(Dispatchers.IO) { runCatching { SyncHolder.close() }.exceptionOrNull() }
                _ui.value = _ui.value.copy(changing = false, lastError = failure?.message)
            }
        }
    }

    /** Hands the pass to [SyncWorker]: a pass owned by the screen dies when the user switches apps. */
    fun syncNow() {
        if (!prefs.folderSync || _ui.value.changing) return
        _ui.value = _ui.value.copy(working = true, lastError = null, state = "syncing")
        SyncWorker.syncNow(getApplication())
    }

    /** True when the last pass stopped because it would have deleted a large share of the files. */
    val bulkDeleteBlocked: Boolean
        get() = _ui.value.lastError?.contains(SyncWorker.BULK_DELETE_MARKER) == true

    /** One pass allowed to carry the deletions the safety check stopped. */
    fun confirmBulkDelete() {
        viewModelScope.launch {
            val failure = withContext(Dispatchers.IO) { runCatching { SyncHolder.use(getApplication()) { it.confirmBulkDelete() } }.exceptionOrNull() }
            if (failure != null) _ui.value = _ui.value.copy(lastError = failure.message)
            else syncNow()
        }
    }

    /**
     * Forgets what this device knows about the server's tree and fetches it again; the folder
     * stays as it is and nothing on the server is touched. The right way out when the folder
     * went missing rather than the files being deleted.
     */
    fun resyncFromServer() {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(working = true, lastError = null, state = "syncing")
            val err = withContext(Dispatchers.IO) {
                runCatching { SyncHolder.use(getApplication()) { it.resetLocalIndex() } }.exceptionOrNull()?.message
            }
            if (err != null) { _ui.value = _ui.value.copy(working = false, lastError = err); return@launch }
            syncNow()
        }
    }

    private fun watchSyncWork() {
        viewModelScope.launch {
            WorkManager.getInstance(getApplication<Application>())
                .getWorkInfosForUniqueWorkFlow(SyncWorker.MANUAL_NAME)
                .collect { infos ->
                    if (!prefs.folderSync) return@collect
                    val info = infos.lastOrNull() ?: return@collect
                    if (!info.state.isFinished) {
                        _ui.value = _ui.value.copy(working = true, state = "syncing")
                        return@collect
                    }
                    refreshStatus(info.outputData.getString(SyncWorker.KEY_ERROR))
                }
        }
    }

    private fun refreshStatus(workerError: String?) {
        if (!prefs.folderSync) return
        viewModelScope.launch {
            val result = withContext(Dispatchers.IO) { runCatching { SyncHolder.use(getApplication()) { it.status() } } }
            if (!prefs.folderSync) return@launch
            val st = result.getOrNull()
            _ui.value = _ui.value.copy(
                working = false,
                state = st?.state ?: _ui.value.state,
                lastSyncUnix = st?.lastSyncUnix ?: _ui.value.lastSyncUnix,
                setAside = st?.setAside?.takeIf { it.isNotEmpty() } ?: _ui.value.setAside,
                lastError = workerError ?: result.exceptionOrNull()?.message ?: st?.lastError?.takeIf { it.isNotEmpty() },
            )
        }
    }
}
