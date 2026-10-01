package org.discodrive.fastsync

import android.app.Application
import android.os.Build
import android.os.Environment
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CoroutineScope
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
    /** A certificate the system rejected, waiting for the user to trust it or not. */
    val certificate: CertDetails? = null,
    // Where the first pass after pairing moved the folder's previous contents, if any.
    val setAside: String? = null,
)

class SyncViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private var resuming = false

    private val _ui = MutableStateFlow(UiState())
    val ui: StateFlow<UiState> = _ui.asStateFlow()

    val syncDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDriveFastSync/Sync")
    private val stateDbPath: String get() = File(getApplication<Application>().filesDir, "state.db").path

    init { refreshAfterPermission(); watchSyncWork(); showUnpairWipe() }

    /** Busy while an unpair's local wipe runs, including one started by an earlier screen. */
    private fun showUnpairWipe() {
        // No isActive check: a wipe that already finished joins at once and still clears the
        // busy state unpair() set.
        val wipe = ClientHolder.unpairWipe ?: return
        _ui.value = _ui.value.copy(working = true)
        viewModelScope.launch {
            wipe.join()
            _ui.value = _ui.value.copy(working = false)
        }
    }

    fun hasStoragePermission(): Boolean = Environment.isExternalStorageManager()

    // Open the client when we have permission + a saved token (called on launch and on returning
    // from the All-files-access settings screen).
    fun refreshAfterPermission() {
        val token = prefs.deviceToken
        if (!_ui.value.paired && token != null && prefs.serverURL.isNotEmpty() && hasStoragePermission()) {
            openClient()
            return
        }
        // Not paired yet — but a pairing may be outstanding, approved while the app was away
        // or killed. Picking it up here turns a lost pairing into a finished one without the
        // user starting over.
        if (token == null && !resuming && _ui.value.pendingUserCode == null) resumePendingPairing()
    }

    private fun openClient() {
        try {
            ClientHolder.get(getApplication()) ?: error("not paired")
            _ui.value = _ui.value.copy(paired = true)
        } catch (e: Exception) {
            _ui.value = _ui.value.copy(lastError = e.message)
        }
    }

    /** The server and certificate the trust dialog is showing; only this pin may be used. */
    private var certOffer: Pair<String, CertDetails>? = null

    // openUrl is invoked (on the main thread) after PairBegin so the UI can open the browser
    // at the verification URL before PairAwait blocks.
    fun pair(server: String, openUrl: (String) -> Unit) {
        certOffer = null
        beginPairing(server.trim(), "", openUrl)
    }

    /** The user trusted the certificate on screen: pair again, accepting exactly that one. */
    fun trustCertificate(openUrl: (String) -> Unit) {
        val (server, cert) = certOffer ?: return
        certOffer = null
        _ui.value = _ui.value.copy(certificate = null)
        beginPairing(server, cert.fingerprint, openUrl)
    }

    fun rejectCertificate() {
        certOffer = null
        _ui.value = _ui.value.copy(certificate = null)
    }

    private fun beginPairing(server: String, pin: String, openUrl: (String) -> Unit) {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(working = true, lastError = null, certificate = null)
            // An unpair still wiping the previous index must finish first, or it wipes this one.
            ClientHolder.unpairWipe?.join()
            try {
                val p = try {
                    withContext(Dispatchers.IO) { SyncCore.pairBegin(server, Build.MODEL, "android", pin) }
                } catch (e: Exception) {
                    // Strict pairing failed. If the reason is a certificate the system does not
                    // trust, offer it; any other failure is reported as it is.
                    val offer = if (pin.isEmpty()) untrustedCertificate(server) else null
                    if (offer == null) throw e
                    certOffer = server to offer
                    _ui.value = _ui.value.copy(certificate = offer)
                    return@launch
                }
                val pending = PendingPairing(server, p.deviceCode, p.userCode, p.intervalSeconds, pin)
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

    private suspend fun untrustedCertificate(server: String): CertDetails? = withContext(Dispatchers.IO) {
        CertTrust.offerFor(runCatching { SyncCore.fetchCertificate(server) }.getOrNull())
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
                SyncCore.pairAwait(pending.server, pending.deviceCode, pending.intervalSeconds, pending.pin)
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
            prefs.saveServer(pending.server, token, pending.pin)
            prefs.pendingPairing = null
            openClient()
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
                setAside = st?.setAside?.takeIf { it.isNotEmpty() } ?: _ui.value.setAside,
                lastError = workerError ?: st?.lastError?.takeIf { it.isNotEmpty() },
            )
        }
    }

    fun unpair() {
        val server = prefs.serverURL
        val token = prefs.deviceToken
        val pin = prefs.serverPin
        prefs.clear()
        SyncWorker.cancel(getApplication())
        // Busy until the wipe is done: a pairing started before it would have its fresh index
        // wiped. beginPairing waits for the wipe too, from any screen (the job is process-wide).
        _ui.value = UiState(working = true)
        val app = getApplication<Application>()
        // Outside the screen's scope: leaving the screen must not skip the wipe. Off the main
        // thread: closing waits for a pass in flight to finish. The index goes with it — one that
        // outlives the pairing describes files this device no longer has, and the next pass
        // would push their absence as deletions on the server.
        val wipe = CoroutineScope(Dispatchers.IO).launch { ClientHolder.wipe(app) }
        ClientHolder.unpairWipe = wipe
        showUnpairWipe()
        // End the device on the server too: forgotten only here, the token kept working for
        // anyone holding a copy. It runs after the wipe with the token captured above and
        // detached, so offline unpairing completes and never holds up a new pairing. The call
        // blocks; its own 15 s timeout (mobile.RevokeDevice) is what bounds it.
        if (server.isNotEmpty() && !token.isNullOrEmpty()) {
            CoroutineScope(Dispatchers.IO).launch {
                wipe.join()
                runCatching { SyncCore.revokeDevice(server, token, pin) }
            }
        }
    }
}
