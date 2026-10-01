package org.discodrive.android

import android.app.Application
import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import mobile.Vault
import org.json.JSONArray
import java.io.File

data class VEntry(val name: String, val isDir: Boolean, val dirID: String, val fileStoragePath: String)
data class VFolder(val dirID: String, val name: String)
data class VaultState(
    val open: Boolean = false,
    val loading: Boolean = false,
    val error: String? = null,
    val stack: List<VFolder> = listOf(VFolder("", "Vault")),
    val entries: List<VEntry> = emptyList(),
)

class VaultViewModel(app: Application) : AndroidViewModel(app) {
    private var vault: Vault? = null
    private val _ui = MutableStateFlow(VaultState())
    val ui: StateFlow<VaultState> = _ui.asStateFlow()

    private val indexDbPath: String get() = File(getApplication<Application>().filesDir, "vault-index.db").path
    private val tmpDir: String get() = File(getApplication<Application>().cacheDir, "vault").path

    fun open(server: String, token: String, vaultRoot: String, password: String, serverPin: String) {
        if (vault != null || _ui.value.loading) return
        viewModelScope.launch {
            _ui.value = VaultState(loading = true)
            try {
                val v = withContext(Dispatchers.IO) {
                    val previous = File(tmpDir)
                    check(!previous.exists() || previous.deleteRecursively()) { "Could not clear previous vault previews" }
                    Core.openVault(server, token, vaultRoot, password, indexDbPath, tmpDir, serverPin)
                }
                vault = v
                _ui.value = _ui.value.copy(open = true, loading = false, stack = listOf(VFolder("", getApplication<Application>().getString(R.string.vault_root))))
                reload()
            } catch (e: Exception) {
                _ui.value = VaultState(open = false, error = e.message)
            }
        }
    }

    fun atRoot(): Boolean = _ui.value.stack.size <= 1
    private fun currentDirID(): String = _ui.value.stack.last().dirID

    fun enter(e: VEntry) {
        _ui.value = _ui.value.copy(stack = _ui.value.stack + VFolder(e.dirID, e.name))
        reload()
    }

    fun back() {
        if (atRoot()) return
        _ui.value = _ui.value.copy(stack = _ui.value.stack.dropLast(1))
        reload()
    }

    fun reload() {
        viewModelScope.launch {
            val v = vault ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val js = withContext(Dispatchers.IO) { v.list(currentDirID()) }
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    suspend fun preview(e: VEntry): String {
        val current = vault ?: error("Vault is closed")
        return withContext(Dispatchers.IO) { current.openFile(e.fileStoragePath, e.name) }
    }

    fun openFile(e: VEntry, then: (String) -> Unit) {
        viewModelScope.launch {
            val v = vault ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val path = withContext(Dispatchers.IO) { v.openFile(e.fileStoragePath, e.name) }
                then(path)
            } catch (ex: Exception) {
                _ui.value = _ui.value.copy(error = ex.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    private fun op(block: suspend (Vault) -> Unit) {
        viewModelScope.launch {
            val v = vault ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                withContext(Dispatchers.IO) { block(v) }
                val js = withContext(Dispatchers.IO) { v.list(currentDirID()) }
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    fun mkdir(name: String) = op { it.mkdir(currentDirID(), name) }
    fun delete(e: VEntry) = op { it.remove(currentDirID(), e.name) }

    fun uploadUri(uri: Uri) {
        val ctx = getApplication<Application>()
        viewModelScope.launch {
            val v = vault ?: return@launch
            _ui.value = _ui.value.copy(loading = true, error = null)
            try {
                val name = displayName(ctx, uri)
                // The name comes from another app; a separator or ".." in it must not reach
                // the vault's file layer.
                require(isValidUploadName(name)) { "Invalid file name" }
                withContext(Dispatchers.IO) {
                    val tmp = File.createTempFile("vault-upload-", ".tmp", ctx.cacheDir)
                    try {
                        ctx.contentResolver.openInputStream(uri)!!.use { input -> tmp.outputStream().use { input.copyTo(it) } }
                        v.writeFile(currentDirID(), name, tmp.path)
                    } finally { tmp.delete() }
                }
                val js = withContext(Dispatchers.IO) { v.list(currentDirID()) }
                _ui.value = _ui.value.copy(entries = parse(js))
            } catch (e: Exception) {
                _ui.value = _ui.value.copy(error = e.message)
            } finally {
                _ui.value = _ui.value.copy(loading = false)
            }
        }
    }

    fun lock() {
        if (_ui.value.loading) return
        val current = vault ?: return
        _ui.value = _ui.value.copy(loading = true)
        viewModelScope.launch {
            try {
                withContext(Dispatchers.IO) { current.close() }
                vault = null
                _ui.value = VaultState()
            } catch (e: Exception) { _ui.value = _ui.value.copy(loading = false, error = e.message) }
        }
    }

    // Leaving the vault screen without locking (back out of the app, swipe it away) must
    // not leave decrypted previews in the cache or the vault handle open.
    override fun onCleared() {
        val current = vault ?: return
        vault = null
        Thread { runCatching { current.close() } }.start()
        super.onCleared()
    }

    // dismissError clears a failed-open state (e.g. wrong password) and returns to the browser.
    fun dismissError() { _ui.value = VaultState() }

    private fun parse(json: String): List<VEntry> {
        val arr = JSONArray(json)
        val out = ArrayList<VEntry>(arr.length())
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            out.add(VEntry(o.getString("name"), o.getBoolean("isDir"), o.optString("dirID"), o.optString("fileStoragePath")))
        }
        return out
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
