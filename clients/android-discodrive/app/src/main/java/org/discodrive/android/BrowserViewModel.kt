package org.discodrive.android

import android.app.Application
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Browser
import androidx.work.WorkManager
import org.discodrive.android.autoupload.AutoUploadWorker
import org.discodrive.android.autoupload.FolderObservers
import org.discodrive.android.autoupload.PairingGeneration
import org.discodrive.android.autoupload.UploadJournal
import org.discodrive.android.sync.SyncEvents
import org.discodrive.android.sync.SyncHolder
import org.discodrive.android.sync.SyncWorker
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

data class Entry(
    val id: String, val name: String, val isDir: Boolean, val size: Long, val version: Long,
    val cached: Boolean, val pinned: Boolean, val stale: Boolean, val localPath: String,
)

data class Folder(val id: String, val name: String)

data class BrowseState(
    val paired: Boolean = false,
    val loading: Boolean = false,
    /** A pull from the server is in flight; the list on screen comes from the local index. */
    val syncing: Boolean = false,
    val error: String? = null,
    val pendingUserCode: String? = null,
    /**
     * The approval link the server sent, shown as text because it did not pass
     * [UrlPolicy.verificationUrlAllowed] and so was not opened; null otherwise.
     */
    val verificationUrl: String? = null,
    /** A certificate the system rejected, waiting for the user to trust it or not. */
    val certificate: CertDetails? = null,
    val stack: List<Folder> = listOf(Folder("", "DiscoDrive")),
    val entries: List<Entry> = emptyList(),
)

class BrowserViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private var opened = false
    private var opening = false
    private var resuming = false

    // Paired is a fact about the stored token, known before anything touches the network.
    private val _ui = MutableStateFlow(BrowseState(paired = isPaired(), loading = isPaired()))
    val ui: StateFlow<BrowseState> = _ui.asStateFlow()

    val rootDir: File = File(app.filesDir, "browser-cache")
    private val indexDbPath: String get() = File(getApplication<Application>().filesDir, "index.db").path

    init {
        // An unpair that did not finish (a close timed out, the process died) left the app
        // half-unpaired: token still stored, every holder refusing to open. Finish it.
        if (prefs.unpairing) finishUnpair() else openIfPaired()
        watchRefreshWork()
    }

    fun hasStoragePermission(): Boolean = Environment.isExternalStorageManager()

    private fun isPaired(): Boolean = prefs.deviceToken != null && prefs.serverURL.isNotEmpty()

    /**
     * Called on every return to the app — after the storage permission, and after the pairing
     * browser. Opening the index performs no request, so repeating it is cheap and is how the
     * app recovers; the part that can fail, [syncNow], reports its own failure without
     * touching whether the device counts as paired. An attempt already in flight is not
     * duplicated.
     */
    fun openIfPaired() {
        if (prefs.unpairing) return
        if (!isPaired()) {
            // Not paired yet — but a pairing may be outstanding, approved while the app was
            // away or killed. Picking it up here is what turns a lost pairing into a finished
            // one without the user starting over.
            if (!resuming && _ui.value.pendingUserCode == null) resumePendingPairing()
            return
        }
        if (opened || opening) return
        openBrowser()
    }

    /**
     * Borrows the shared browser for one operation, off the main thread. Borrowing rather than
     * holding onto it is what keeps re-pairing — which closes it — from cutting an operation
     * off mid-flight ("sql: database is closed"). Null when the device is not paired.
     */
    private suspend fun <T> withBrowser(block: (Browser) -> T): T? {
        val token = prefs.deviceToken
        val server = prefs.serverURL
        val result = withContext(Dispatchers.IO) { BrowserHolder.use(getApplication(), block) }
        return if (!prefs.unpairing && token == prefs.deviceToken && server == prefs.serverURL) result else null
    }

    /**
     * Opens the local index, shows what it already holds, and only then pulls from the server.
     *
     * Being paired used to be decided here, by whether that pull succeeded. So every launch
     * flashed the pairing screen on the way to the file list — and a pull that failed or hung
     * (a connection killed while the app was backgrounded) left the app sitting on it, with
     * the pairing already done and repeating it changing nothing.
     */
    private fun openBrowser() {
        opening = true
        _ui.value = _ui.value.copy(loading = true)
        viewModelScope.launch {
            try {
                // Listed inline rather than through reload(), which runs in a coroutine of its
                // own: its result could land after the pull's and put the pre-pull list back.
                val js = withBrowser { it.list("") } ?: return@launch
                opened = true
                _ui.value = _ui.value.copy(
                    paired = true, loading = false, error = null, entries = parse(js),
                    stack = listOf(Folder("", getApplication<Application>().getString(R.string.app_name))),
                )
                syncNow()
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                opening = false
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    /**
     * Pulls change-feed metadata into the index and relists. The list stays readable while it
     * runs, and a failure is reported without hiding what is already there — the toolbar's
     * refresh is how the user retries.
     *
     * Hands the work to [RefreshWorker] rather than running it here.
     *
     * A pull owned by the screen ended the moment the user switched apps: the process is
     * cached once its UI is gone and loses the network, DNS first ("lookup <host>: no such
     * host"). The worker promotes itself to the foreground, so a long first pull survives.
     */
    fun syncNow() {
        if (!isPaired()) return
        _ui.value = _ui.value.copy(syncing = true, error = null)
        RefreshWorker.start(getApplication())
    }

    /** Follows the pull and relists once it is done, whoever started it. */
    private fun watchRefreshWork() {
        viewModelScope.launch {
            WorkManager.getInstance(getApplication<Application>())
                .getWorkInfosForUniqueWorkFlow(RefreshWorker.NAME)
                .collect { infos ->
                    if (!isPaired() || prefs.unpairing) return@collect
                    val info = infos.lastOrNull() ?: return@collect
                    if (!info.state.isFinished) {
                        _ui.value = _ui.value.copy(syncing = true)
                        return@collect
                    }
                    val err = info.outputData.getString(RefreshWorker.KEY_ERROR)
                    _ui.value = _ui.value.copy(syncing = false, error = err ?: _ui.value.error)
                    if (err == null && opened) reload()
                }
        }
    }

    /** The server and certificate the trust dialog is showing; only this pin may be used. */
    private var certOffer: Pair<String, CertDetails>? = null

    fun pair(serverInput: String, openUrl: (String) -> Unit) {
        val server = serverInput.trim()
        // The Go client ignores Android's cleartext policy: an http server would get the
        // device token in the clear.
        if (!UrlPolicy.serverUrlAllowed(server)) {
            _ui.value = _ui.value.copy(error = getApplication<Application>().getString(R.string.setup_https_required))
            return
        }
        certOffer = null
        beginPairing(server, "", openUrl)
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
            _ui.value = _ui.value.copy(loading = true, error = null, verificationUrl = null, certificate = null)
            try {
                val p = try {
                    withContext(Dispatchers.IO) { Core.pairBegin(server, Build.MODEL, "android", pin) }
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
                // The link comes from the server and ACTION_VIEW hands it to whatever app
                // claims its scheme; anything but a web page on this server is only shown.
                if (UrlPolicy.verificationUrlAllowed(p.verificationURL, server)) {
                    openUrl(p.verificationURL)
                } else {
                    _ui.value = _ui.value.copy(verificationUrl = p.verificationURL)
                }
                awaitApproval(pending)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message, pendingUserCode = null, verificationUrl = null)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    private suspend fun untrustedCertificate(server: String): CertDetails? = withContext(Dispatchers.IO) {
        val fetched = runCatching { Core.fetchCertificate(server) }
        // The pairing error is what the user sees; why the certificate could not be read
        // goes to the opt-in diagnostics log.
        fetched.exceptionOrNull()?.let {
            Diagnostics.record(getApplication(), JSONObject().put("event", "fetch_certificate").toString(), it.message)
        }
        CertTrust.offerFor(fetched.getOrNull())
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
            // Started by an older version that still accepted http: not finished over it.
            if (!UrlPolicy.serverUrlAllowed(pending.server)) {
                withContext(Dispatchers.IO) { prefs.pendingPairing = null }
                resuming = false
                return@launch
            }
            _ui.value = _ui.value.copy(loading = true, error = null, pendingUserCode = pending.userCode)
            try {
                awaitApproval(pending)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message, pendingUserCode = null)
            } finally {
                resuming = false
                _ui.value = _ui.value.copy(loading = false)
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
                Core.pairAwait(pending.server, pending.deviceCode, pending.intervalSeconds, pending.pin)
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
            SyncHolder.close()
            BrowserHolder.close()
            prefs.saveServer(pending.server, token, pending.pin)
            prefs.pendingPairing = null
            DriveDocumentsProvider.notifyRoots(getApplication())
        }
        // The process-wide holder may still carry a Browser built on the previous device token
        // — opening one performs no request, so a dead token lives in it until something asks
        // the server. Left alone it 401s straight through a successful re-pairing, for the rest
        // of the process's life. Closing waits for any pass still using the index (an
        // auto-upload batch, the previous refresh): closing under one surfaced as "sql:
        // database is closed" in the middle of a pairing that had otherwise succeeded.
        opened = false
        // Paired, settled by the token the server just issued. Waiting for the first successful
        // pull instead stranded the user on this screen whenever that pull failed — the pairing
        // itself had gone through, so pairing again did nothing.
        _ui.value = _ui.value.copy(pendingUserCode = null, verificationUrl = null, paired = true)
        openBrowser()
    }

    private fun parse(json: String): List<Entry> {
        val arr = JSONArray(json)
        val out = ArrayList<Entry>(arr.length())
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            out.add(
                Entry(
                    o.getString("id"), o.getString("name"), o.getBoolean("isDir"),
                    o.optLong("size"), o.optLong("version"), o.optBoolean("cached"),
                    o.optBoolean("pinned"), o.optBoolean("stale"), o.optString("localPath")
                )
            )
        }
        return out
    }

    fun atRoot(): Boolean = _ui.value.stack.size <= 1
    private fun currentId(): String = _ui.value.stack.last().id

    val server: String get() = prefs.serverURL
    val token: String? get() = prefs.deviceToken
    val serverPin: String get() = prefs.serverPin

    // currentFolderIsVault: the currently-listed folder is a Cryptomator vault.
    fun currentFolderIsVault(): Boolean = _ui.value.entries.any { it.name == "masterkey.cryptomator" }

    // currentRelPath: rel_path of the current folder ("" at root) — used as vaultRoot when
    // unlocking. Off the main thread: the shared browser can be held for minutes by an
    // upload batch or a refresh, and waiting for it on the UI thread froze the app (ANR).
    suspend fun currentRelPath(): String? = resolveSelectedFolder(
        currentFolder = { currentId() },
        resolve = { id ->
            withContext(Dispatchers.IO) {
                BrowserHolder.use(getApplication()) { it.relPath(id) }
            }
        },
    )

    fun enter(e: Entry) {
        _ui.value = _ui.value.copy(stack = _ui.value.stack + Folder(e.id, e.name))
        reload()
    }

    fun back() {
        if (atRoot()) return
        _ui.value = _ui.value.copy(stack = _ui.value.stack.dropLast(1))
        reload()
    }

    fun reload() {
        if (!opened) return
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val js = withBrowser { it.list(currentId()) } ?: return@launch
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    private fun op(block: (Browser) -> Unit) {
        if (!opened) return
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                // One borrow for both: the listing must see what the operation just did.
                val js = withBrowser { block(it); it.list(currentId()) } ?: return@launch
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                failed(e)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    fun pin(id: String) = op { it.pin(id) }
    fun unpin(id: String) = op { it.unpin(id) }
    fun removeLocal(id: String) = op { it.removeLocal(id) }
    fun download(id: String) = op { it.download(id) }
    fun delete(id: String) = op { it.delete(id) }
    fun mkdir(name: String) = op { it.mkdir(currentId(), name) }
    fun rename(id: String, name: String) = op { it.rename(id, name) }
    fun move(id: String, destId: String) = op { it.move(id, destId) }

    // open: download (if needed) then hand the local path to the caller (FileProvider ACTION_VIEW).
    suspend fun preview(id: String): String = withBrowser { it.download(id) } ?: error("Not paired")

    fun open(id: String, then: (String) -> Unit) {
        if (!opened) return
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val path = withBrowser { it.download(id) } ?: return@launch
                then(path)
                val js = withBrowser { it.list(currentId()) } ?: return@launch
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                failed(e)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    /**
     * Reports an operation's failure. An item the server no longer has is already out of the
     * index by now (the core applied the delete it missed): relist, and say so in words
     * instead of showing the raw 404.
     */
    private suspend fun failed(e: Exception) {
        if (e is kotlinx.coroutines.CancellationException) throw e
        if (NodeGone.matches(e.message)) {
            runCatching { withBrowser { it.list(currentId()) } }.getOrNull()?.let {
                _ui.value = _ui.value.copy(entries = parse(it))
            }
        }
        val gone = getApplication<Application>().getString(R.string.error_node_gone)
        _ui.value = _ui.value.copy(error = NodeGone.describe(e.message, gone))
    }

    fun uploadUri(uri: Uri) {
        val ctx = getApplication<Application>()
        if (!opened) return
        val parent = currentId()
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            var staging: File? = null
            try {
                val tmp = withContext(Dispatchers.IO) {
                    val name = displayName(ctx, uri)
                    require(isValidUploadName(name)) { "Invalid file name" }
                    val directory = java.nio.file.Files.createTempDirectory(ctx.cacheDir.toPath(), "upload-").toFile()
                    staging = directory
                    File(directory, name).also { file ->
                        val source = ctx.contentResolver.openInputStream(uri) ?: error("Could not open file")
                        source.use { input -> file.outputStream().use { input.copyTo(it) } }
                    }
                }
                val js = withBrowser { it.upload(tmp.path, parent); it.list(parent) } ?: return@launch
                if (currentId() == parent) _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                staging?.deleteRecursively()
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    // For the Move picker: returns only the folder children of folderId.
    suspend fun listFolders(folderId: String): List<Entry> {
        val js = withBrowser { it.list(folderId) } ?: return emptyList()
        return parse(js).filter { it.isDir }
    }

    fun unpair() {
        if (_ui.value.loading) return
        prefs.unpairing = true
        finishUnpair()
    }

    /**
     * Everything after the "unpairing" flag is set. Also run at start-up when that flag is
     * found still set: a previous attempt failed half-way (a holder could not close in time,
     * the process was killed), and the app must not stay stuck between paired and not.
     */
    private fun finishUnpair() {
        _ui.value = _ui.value.copy(loading = true, error = null)
        // Unpairing must also stop auto-upload: its rules point at a server this device no
        // longer has a token for, and a scheduled pass would keep failing in the background.
        prefs.autoUpload = false
        AutoUploadWorker.cancel(getApplication())
        FolderObservers.get(getApplication()).stop()
        // Folder sync goes the same way: its engine is bound to this pairing.
        prefs.folderSync = false
        SyncWorker.cancel(getApplication())
        SyncEvents.stop(getApplication())
        RefreshWorker.cancel(getApplication())
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) {
                    // End the device on the server first, while the token is still known:
                    // forgotten only here, it kept working for anyone holding a copy.
                    // Offline, unpairing still completes locally.
                    val server = prefs.serverURL
                    val token = prefs.deviceToken
                    if (server.isNotEmpty() && !token.isNullOrEmpty()) {
                        runCatching { Core.revokeDevice(server, token, prefs.serverPin) }
                    }
                    SyncHolder.wipe(getApplication())
                    BrowserHolder.wipe(getApplication())
                    // The journal records what went to THIS server; kept, it would stop
                    // every one of those photos from ever reaching the next one.
                    // Under the pairing generation: a folder scan still running from
                    // before must not write its journal entries and rule back afterwards.
                    PairingGeneration.end {
                        UploadJournal.wipe(getApplication())
                        prefs.clear()
                    }
                    DriveDocumentsProvider.notifyRoots(getApplication())
                }
                opened = false
                opening = false
                _ui.value = BrowseState()
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(loading = false, error = e.message)
            }
        }
    }

    /** Shown on the settings row; the auto-upload screen owns everything else. */
    val autoUploadOn: Boolean get() = prefs.autoUpload
    /** Shown on the settings row; the folder-sync screen owns everything else. */
    val folderSyncOn: Boolean get() = prefs.folderSync
}

private fun displayName(ctx: Context, uri: Uri): String {
    var name = "upload"
    ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
        if (c.moveToFirst()) {
            val idx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
            if (idx >= 0) name = c.getString(idx)
        }
    }
    return name
}
