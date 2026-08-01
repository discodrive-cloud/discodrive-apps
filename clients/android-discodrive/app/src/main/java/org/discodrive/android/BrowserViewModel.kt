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
import org.discodrive.android.autoupload.AutoUploadRunner
import org.discodrive.android.autoupload.Block
import org.discodrive.android.autoupload.AutoUploadWorker
import org.discodrive.android.autoupload.Conditions
import org.discodrive.android.autoupload.FolderObservers
import org.discodrive.android.autoupload.RunResult
import org.discodrive.android.autoupload.Rule
import org.discodrive.android.autoupload.UploadJournal
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
    val error: String? = null,
    val pendingUserCode: String? = null,
    val stack: List<Folder> = listOf(Folder("", "DiscoDrive")),
    val entries: List<Entry> = emptyList(),
)

class BrowserViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = Prefs(app)
    private var browser: Browser? = null

    private val _ui = MutableStateFlow(BrowseState())
    val ui: StateFlow<BrowseState> = _ui.asStateFlow()

    val rootDir: File = File(Environment.getExternalStorageDirectory(), "DiscoDrive")
    private val indexDbPath: String get() = File(getApplication<Application>().filesDir, "index.db").path

    init { refreshAfterPermission() }

    fun hasStoragePermission(): Boolean = Environment.isExternalStorageManager()

    fun refreshAfterPermission() {
        val token = prefs.deviceToken
        if (browser == null && token != null && prefs.serverURL.isNotEmpty() && hasStoragePermission()) {
            openBrowser(prefs.serverURL, token, prefs.insecure)
        }
    }

    private fun openBrowser(server: String, token: String, insecure: Boolean) {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val b = withContext(Dispatchers.IO) {
                    // One browser per process: the auto-upload service shares it, and a
                    // second handle on the index file fails with SQLITE_BUSY.
                    val br = BrowserHolder.get(getApplication()) ?: error("not paired")
                    br.refresh()
                    br
                }
                browser = b
                _ui.value = _ui.value.copy(paired = true, stack = listOf(Folder("", getApplication<Application>().getString(R.string.app_name))))
                reload()
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    fun pair(server: String, insecure: Boolean, openUrl: (String) -> Unit) {
        viewModelScope.launch {
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val p = withContext(Dispatchers.IO) { Core.pairBegin(server, Build.MODEL, "android", insecure) }
                _ui.value = _ui.value.copy(pendingUserCode = p.userCode)
                openUrl(p.verificationURL)
                val token = withContext(Dispatchers.IO) { Core.pairAwait(server, p.deviceCode, p.intervalSeconds, insecure) }
                prefs.serverURL = server; prefs.insecure = insecure; prefs.deviceToken = token
                _ui.value = _ui.value.copy(pendingUserCode = null)
                openBrowser(server, token, insecure)
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message, pendingUserCode = null)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
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
    fun currentRelPath(): String = if (atRoot()) "" else (browser?.relPath(currentId()) ?: "")

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
        viewModelScope.launch {
            val b = browser ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val js = withContext(Dispatchers.IO) { b.list(currentId()) }
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    private fun op(block: suspend (Browser) -> Unit) {
        viewModelScope.launch {
            val b = browser ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                withContext(Dispatchers.IO) { block(b) }
                val js = withContext(Dispatchers.IO) { b.list(currentId()) }
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
        viewModelScope.launch {
            val b = browser ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val path = withContext(Dispatchers.IO) { b.download(id) }
                then(path)
                val js = withContext(Dispatchers.IO) { b.list(currentId()) }
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
        viewModelScope.launch {
            val b = browser ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val tmp = withContext(Dispatchers.IO) {
                    val name = displayName(ctx, uri)
                    val f = File(ctx.cacheDir, name)
                    ctx.contentResolver.openInputStream(uri)!!.use { input -> f.outputStream().use { input.copyTo(it) } }
                    f
                }
                withContext(Dispatchers.IO) { b.upload(tmp.path, currentId()) }
                tmp.delete()
                val js = withContext(Dispatchers.IO) { b.list(currentId()) }
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
        val b = browser ?: return emptyList()
        val js = withContext(Dispatchers.IO) { b.list(folderId) }
        return parse(js).filter { it.isDir }
    }

    fun unpair() {
        disableAutoUploadTriggers()
        BrowserHolder.close()
        browser = null
        prefs.clear()
        _ui.value = BrowseState()
    }

    // --- auto-upload (temporary manual trigger; the service and its screen land next) ---

    private val _autoUpload = MutableStateFlow<String?>(null)
    val autoUploadStatus: StateFlow<String?> = _autoUpload.asStateFlow()

    /**
     * Starts a pass in the foreground service — the same path the background triggers will
     * use, so what gets verified on a device is what actually ships. Temporary entry point
     * until the auto-upload screen lands.
     */
    fun startAutoUploadPass() {
        val ctx = getApplication<Application>()
        prefs.autoUpload = true
        ensureDefaultRule()
        enableAutoUploadTriggers()
        AutoUploadWorker.runNow(ctx, prefs.wifiOnly)
        _autoUpload.value = "pass enqueued — see the notification"
    }

    /** Watches the rule folders and schedules the periodic catch-up run. */
    fun enableAutoUploadTriggers() {
        val ctx = getApplication<Application>()
        AutoUploadWorker.schedule(ctx, prefs.wifiOnly)
        observers.start()
    }

    fun disableAutoUploadTriggers() {
        val ctx = getApplication<Application>()
        AutoUploadWorker.cancel(ctx)
        observers.stop()
    }

    private val observers = FolderObservers(app)

    override fun onCleared() {
        observers.stop()
        super.onCleared()
    }

    /**
     * The camera folder is what the app proposes when auto-upload is first switched on; the
     * folder picker (next task) adds the rest. Adding it here rather than in the runner keeps
     * "which folders" a stored user choice instead of something the pass assumes.
     */
    private fun ensureDefaultRule() {
        if (prefs.rules.isEmpty()) prefs.addRule(AutoUploadRunner.defaultRule())
    }

    /** Folders currently set to upload, for the debug readout and the upcoming screen. */
    fun autoUploadRules(): List<Rule> = prefs.rules

    /** Adds a folder to auto-upload. Returns false when it is already covered. */
    fun addAutoUploadFolder(path: String): Boolean {
        val dir = File(path)
        if (!dir.isDirectory) return false
        return prefs.addRule(
            Rule.of(sourcePath = dir.path, destSegments = AutoUploadRunner.defaultDestFor(dir))
        )
    }

    fun removeAutoUploadFolder(path: String) = prefs.removeRule(path)

    /**
     * Runs one auto-upload pass in-process. Kept alongside the service path because it
     * surfaces the outcome directly in the UI, which is what makes a failure debuggable.
     */
    fun runAutoUploadNow() {
        val b = browser ?: return
        val ctx = getApplication<Application>()
        viewModelScope.launch {
            ensureDefaultRule()
            _autoUpload.value = "starting…"
            val result = withContext(Dispatchers.IO) {
                val journal = UploadJournal(ctx)
                try {
                    val blocked = Conditions.check(ctx, prefs)
                    if (blocked != Block.NONE) return@withContext RunResult(0, 0, 0, blocked = blocked)
                    val runner = AutoUploadRunner(b, journal, prefs)
                    val seeded = runner.seedIfNeeded()
                    if (seeded > 0) {
                        _autoUpload.value = "marked $seeded existing files as already handled"
                    }
                    runner.runOnce(progress = { done, total, name ->
                        _autoUpload.value = "$done/$total · $name"
                    })
                } finally {
                    journal.close()
                }
            }
            _autoUpload.value = when {
                result.error != null -> "error: ${result.error}"
                result.blocked != Block.NONE -> "waiting: ${result.blocked}"
                else -> "uploaded ${result.uploaded}, skipped ${result.skipped}, deferred ${result.deferred}"
            }
        }
    }
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
