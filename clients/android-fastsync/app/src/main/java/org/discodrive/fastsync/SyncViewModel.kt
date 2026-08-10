package org.discodrive.fastsync

import android.app.Application
import android.os.Build
import android.os.Environment
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import androidx.work.WorkManager
import java.io.File

data class UiState(
    val paired: Boolean = false,
    val working: Boolean = false,
    val state: String = "idle",
    val lastSyncUnix: Long = 0,
    val lastError: String? = null,
    val pendingUserCode: String? = null,
)

class SyncViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private var resuming = false

    private val _ui = MutableStateFlow(UiState())
    val ui: StateFlow<UiState> = _ui.asStateFlow()

    val syncDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDriveFastSync/Sync")
    private val stateDbPath: String get() = File(getApplication<Application>().filesDir, "state.db").path

    init { refreshAfterPermission(); watchSyncWork() }

    fun hasStoragePermission(): Boolean = Environment.isExternalStorageManager()

    // Open the client when we have permission + a saved token (called on launch and on returning
    // from the All-files-access settings screen).
    fun refreshAfterPermission() {
        val token = prefs.deviceToken
        if (!_ui.value.paired && token != null && prefs.serverURL.isNotEmpty() && hasStoragePermission()) {
            openClient(prefs.serverURL, token, prefs.insecure)
            return
        }
        // Not paired yet — but a pairing may be outstanding, approved while the app was away
        // or killed. Picking it up here turns a lost pairing into a finished one without the
        // user starting over.
        if (token == null && !resuming && _ui.value.pendingUserCode == null) resumePendingPairing()
    }

    private fun openClient(server: String, token: String, insecure: Boolean) {
        try {
            ClientHolder.get(getApplication()) ?: error("not paired")
            _ui.value = _ui.value.copy(paired = true)
        } catch (e: Exception) {
            _ui.value = _ui.value.copy(lastError = e.message)
        }
    }

    // openUrl is invoked (on the main thread) after PairBegin so the UI can open the browser
    // at the verification URL before PairAwait blocks.
    fun pair(server: String, insecure: Boolean, openUrl: (String) -> Unit) {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(working = true, lastError = null)
            try {
                val p = withContext(Dispatchers.IO) {
                    SyncCore.pairBegin(server, Build.MODEL, "android", insecure)
                }
                val pending = PendingPairing(server, p.deviceCode, p.userCode, p.intervalSeconds, insecure)
                withContext(Dispatchers.IO) { prefs.pendingPairing = pending }
                _ui.value = _ui.value.copy(pendingUserCode = p.userCode)
                openUrl(p.verificationURL)
                awaitApproval(pending)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(lastError = e.message)
            } finally {
                _ui.value = _ui.value.copy(working = false, pendingUserCode = null)
            }
        }
    }

    /**
     * Picks up a pairing that was already started — after the app was killed while the user was
     * off approving it, which is easy to hit because approving happens outside the app and can
     * happen on another device entirely.
     */
    private fun resumePendingPairing() {
        val pending = prefs.pendingPairing ?: return
        resuming = true
        viewModelScope.launch {
            _ui.value = _ui.value.copy(working = true, lastError = null, pendingUserCode = pending.userCode)
            try {
                awaitApproval(pending)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(lastError = e.message)
            } finally {
                resuming = false
                _ui.value = _ui.value.copy(working = false, pendingUserCode = null)
            }
        }
    }

    /**
     * Waits for the server to report the pairing approved, then switches the app over to it.
     * Clears the stored pending pairing either way — a code the server has finished with (used,
     * expired) must not be retried on every launch from here on.
     */
    private suspend fun awaitApproval(pending: PendingPairing) {
        val token = try {
            withContext(Dispatchers.IO) {
                SyncCore.pairAwait(pending.server, pending.deviceCode, pending.intervalSeconds, pending.insecure)
            }
        } catch (e: Exception) {
            // A network failure leaves it pending, to be retried; anything else is the server
            // saying this code is done with.
            if (e.message?.contains("pairing not completed") == true) {
                withContext(Dispatchers.IO) { prefs.pendingPairing = null }
            }
            throw e
        }
        withContext(Dispatchers.IO) {
            prefs.saveServer(pending.server, token, pending.insecure)
            prefs.pendingPairing = null
            openClient(pending.server, token, pending.insecure)
        }
        SyncWorker.schedule(getApplication())
    }

    /**
     * Hands the pass to [SyncWorker] rather than running it here.
     *
     * A pass owned by the screen died the moment the user switched apps: the process is
     * cached once its UI is gone and loses the network, DNS first ("lookup <host>: no such
     * host"). The worker promotes itself to the foreground, so the transfer survives.
     */
    fun syncNow() {
        _ui.value = _ui.value.copy(working = true, lastError = null, state = "syncing")
        SyncWorker.syncNow(getApplication())
    }

    /**
     * True when the last pass stopped because it would have deleted a large share of the
     * synced files — the screen offers to confirm rather than leaving the sync stuck.
     */
    val bulkDeleteBlocked: Boolean
        get() = _ui.value.lastError?.contains(SyncCore.BULK_DELETE_MARKER) == true

    /** Runs one pass that is allowed to carry the deletions the safety check stopped. */
    fun confirmBulkDelete() {
        viewModelScope.launch {
            withContext(Dispatchers.IO) { ClientHolder.use(getApplication()) { it.confirmBulkDelete() } }
            syncNow()
        }
    }

    /**
     * Forgets what this device knows about the server's tree and syncs again, so everything is
     * fetched afresh. The other way out of a blocked sync, and the right one when the folder
     * went missing rather than the files being deleted: nothing on the server is touched.
     */
    fun resyncFromServer() {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(working = true, lastError = null, state = "syncing")
            val err = withContext(Dispatchers.IO) {
                runCatching { ClientHolder.use(getApplication()) { it.resetLocalIndex() } }
                    .exceptionOrNull()?.message
            }
            if (err != null) {
                _ui.value = _ui.value.copy(working = false, lastError = err)
                return@launch
            }
            syncNow()
        }
    }

    /** Follows the manual pass and reports its outcome, whoever started it. */
    private fun watchSyncWork() {
        viewModelScope.launch {
            WorkManager.getInstance(getApplication<Application>())
                .getWorkInfosForUniqueWorkFlow(SyncWorker.MANUAL_NAME)
                .collect { infos ->
                    val info = infos.lastOrNull() ?: return@collect
                    if (!info.state.isFinished) {
                        _ui.value = _ui.value.copy(working = true, state = "syncing")
                        return@collect
                    }
                    refreshStatus(info.outputData.getString(SyncWorker.KEY_ERROR))
                }
        }
    }

    /** Reads the pass's own view of where things stand, so the screen agrees with the core. */
    private fun refreshStatus(workerError: String?) {
        viewModelScope.launch {
            val st = withContext(Dispatchers.IO) {
                ClientHolder.use(getApplication()) { it.status() }
            }
            _ui.value = _ui.value.copy(
                working = false,
                state = st?.state ?: _ui.value.state,
                lastSyncUnix = st?.lastSyncUnix ?: _ui.value.lastSyncUnix,
                lastError = workerError ?: st?.lastError?.takeIf { it.isNotEmpty() },
            )
        }
    }

    fun unpair() {
        prefs.clear()
        SyncWorker.cancel(getApplication())
        _ui.value = UiState()
        // Off the main thread: closing waits for a pass in flight to finish. The index goes
        // with it — one that outlives the pairing describes files this device no longer has,
        // and the next pass would push their absence as deletions on the server.
        viewModelScope.launch { withContext(Dispatchers.IO) { ClientHolder.wipe(getApplication()) } }
    }
}
