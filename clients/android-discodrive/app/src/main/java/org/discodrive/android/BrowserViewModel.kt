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
import org.json.JSONArray
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
    val stack: List<Folder> = listOf(Folder("", "DiscoDrive")),
    val entries: List<Entry> = emptyList(),
)

class BrowserViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private var opened = false
    private var opening = false
    private var resuming = false

    // Paired is a fact about the stored token, known before anything touches the network.
    private val _ui = MutableStateFlow(BrowseState(paired = isPaired()))
    val ui: StateFlow<BrowseState> = _ui.asStateFlow()

    val rootDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDrive")
    private val indexDbPath: String get() = File(getApplication<Application>().filesDir, "index.db").path

    init { openIfPaired(); watchRefreshWork() }

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
        if (!isPaired()) {
            // Not paired yet — but a pairing may be outstanding, approved while the app was
            // away or killed. Picking it up here is what turns a lost pairing into a finished
            // one without the user starting over.
            if (!resuming && _ui.value.pendingUserCode == null) resumePendingPairing()
            return
        }
        if (opened || opening) return
        if (!hasStoragePermission()) return
        openBrowser()
    }

    /**
     * Borrows the shared browser for one operation, off the main thread. Borrowing rather than
     * holding onto it is what keeps re-pairing — which closes it — from cutting an operation
     * off mid-flight ("sql: database is closed"). Null when the device is not paired.
     */
    private suspend fun <T> withBrowser(block: (Browser) -> T): T? =
        withContext(Dispatchers.IO) { BrowserHolder.use(getApplication(), block) }

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
        viewModelScope.launch {
            try {
                // Listed inline rather than through reload(), which runs in a coroutine of its
                // own: its result could land after the pull's and put the pre-pull list back.
                val js = withBrowser { it.list("") } ?: error("not paired")
                opened = true
                _ui.value = _ui.value.copy(
                    paired = true, error = null, entries = parse(js),
                    stack = listOf(Folder("", getApplication<Application>().getString(R.string.app_name))),
                )
                syncNow()
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                opening = false
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

    fun pair(server: String, insecure: Boolean, openUrl: (String) -> Unit) {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val p = withContext(Dispatchers.IO) { Core.pairBegin(server, Build.MODEL, "android", insecure) }
                val pending = PendingPairing(server, p.deviceCode, p.userCode, p.intervalSeconds, insecure)
                withContext(Dispatchers.IO) { prefs.pendingPairing = pending }
                _ui.value = _ui.value.copy(pendingUserCode = p.userCode)
                openUrl(p.verificationURL)
                awaitApproval(pending)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message, pendingUserCode = null)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
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
                Core.pairAwait(pending.server, pending.deviceCode, pending.intervalSeconds, pending.insecure)
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
        }
        // The process-wide holder may still carry a Browser built on the previous device token
        // — opening one performs no request, so a dead token lives in it until something asks
        // the server. Left alone it 401s straight through a successful re-pairing, for the rest
        // of the process's life. Closing waits for any pass still using the index (an
        // auto-upload batch, the previous refresh): closing under one surfaced as "sql:
        // database is closed" in the middle of a pairing that had otherwise succeeded.
        withContext(Dispatchers.IO) { BrowserHolder.close() }
        opened = false
        // Paired, settled by the token the server just issued. Waiting for the first successful
        // pull instead stranded the user on this screen whenever that pull failed — the pairing
        // itself had gone through, so pairing again did nothing.
        _ui.value = _ui.value.copy(pendingUserCode = null, paired = true)
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
    val insecureTLS: Boolean get() = prefs.insecure

    // currentFolderIsVault: the currently-listed folder is a Cryptomator vault.
    fun currentFolderIsVault(): Boolean = _ui.value.entries.any { it.name == "masterkey.cryptomator" }

    // currentRelPath: rel_path of the current folder ("" at root) — used as vaultRoot when unlocking.
    fun currentRelPath(): String =
        if (atRoot()) "" else (BrowserHolder.use(getApplication()) { it.relPath(currentId()) } ?: "")

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
                _ui.value = _ui.value.copy(error = e.message)
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
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    fun uploadUri(uri: Uri) {
        val ctx = getApplication<Application>()
        if (!opened) return
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val tmp = withContext(Dispatchers.IO) {
                    val name = displayName(ctx, uri)
                    val f = File(ctx.cacheDir, name)
                    ctx.contentResolver.openInputStream(uri)!!.use { input -> f.outputStream().use { input.copyTo(it) } }
                    f
                }
                val js = withBrowser { it.upload(tmp.path, currentId()); it.list(currentId()) }
                tmp.delete()
                if (js == null) return@launch
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
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
        // Unpairing must also stop auto-upload: its rules point at a server this device no
        // longer has a token for, and a scheduled pass would keep failing in the background.
        prefs.autoUpload = false
        AutoUploadWorker.cancel(getApplication())
        opened = false
        opening = false
        prefs.clear()
        _ui.value = BrowseState()
        // Off the main thread: closing waits for work in flight to finish. The index goes with
        // it — one that outlives the pairing lists files this device no longer has any claim to.
        viewModelScope.launch { withContext(Dispatchers.IO) { BrowserHolder.wipe(getApplication()) } }
    }

    /** Shown on the settings row; the auto-upload screen owns everything else. */
    val autoUploadOn: Boolean get() = prefs.autoUpload
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
