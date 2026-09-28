package org.discodrive.android

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RecoveryScreen(node: Entry?, onChanged: () -> Unit, onBack: () -> Unit) {
    val context = LocalContext.current.applicationContext
    val scope = rememberCoroutineScope()
    var rows by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var busy by remember { mutableStateOf(false) }
    var mutating by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var confirmation by remember { mutableStateOf<JSONObject?>(null) }
    var emptyConfirm by remember { mutableStateOf(false) }
    var restoreVersion by remember { mutableStateOf<Long?>(null) }
    suspend fun load() {
        val json = withContext(Dispatchers.IO) {
            BrowserHolder.use(context) { if (node == null) it.trash() else it.versions(node.id) }
                ?: error("Not paired")
        }
        val array = JSONArray(json)
        // Rows missing what the list keys on are dropped here: a strict read during
        // composition would crash the screen on one incomplete server row.
        rows = List(array.length()) { array.optJSONObject(it) }.filterNotNull()
            .filter { if (node == null) it.optString("id").isNotEmpty() && it.has("name") else it.has("version") && !it.isNull("version") }
    }
    fun action(block: (mobile.Browser) -> Unit) {
        if (busy) return
        scope.launch {
            busy = true; mutating = true; error = null
            try {
                withContext(Dispatchers.IO) { BrowserHolder.use(context, block) ?: error("Not paired") }
                load(); onChanged()
            } catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message }
            finally { busy = false; mutating = false }
        }
    }
    LaunchedEffect(node?.id) {
        busy = true
        try { load() } catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message } finally { busy = false }
    }
    BackHandler { if (!mutating) onBack() }
    Scaffold(topBar = {
        TopAppBar(title = { Text(stringResource(if (node == null) R.string.recovery_trash else R.string.recovery_versions)) },
            navigationIcon = { IconButton(onClick = onBack, enabled = !mutating) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) } })
    }) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) }
            if (node != null) {
                Text(node.name, modifier = Modifier.padding(16.dp), style = MaterialTheme.typography.titleMedium)
                Text(stringResource(R.string.recovery_version_hint), modifier = Modifier.padding(horizontal = 16.dp))
            } else if (rows.isNotEmpty()) {
                TextButton(onClick = { emptyConfirm = true }, enabled = !busy) { Text(stringResource(R.string.recovery_empty)) }
            }
            if (rows.isEmpty() && !busy && error == null) Text(stringResource(if (node == null) R.string.recovery_empty_trash else R.string.recovery_empty_versions), Modifier.padding(16.dp))
            LazyColumn(Modifier.fillMaxSize()) {
                items(rows, key = { if (node == null) it.getString("id") else it.getLong("version").toString() }) { row ->
                    Column(Modifier.fillMaxWidth().padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(if (node == null) row.getString("name") else stringResource(R.string.recovery_version) + " " + row.getLong("version"), style = MaterialTheme.typography.titleMedium)
                        if (row.optBoolean("is_conflict_loser")) Text(stringResource(R.string.recovery_conflict))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            OutlinedButton(enabled = !busy, onClick = {
                                if (node == null) action { it.undelete(row.getString("id")) }
                                else restoreVersion = row.getLong("version")
                            }) { Text(stringResource(R.string.recovery_restore)) }
                            if (node == null) TextButton(enabled = !busy, onClick = { confirmation = row }) { Text(stringResource(R.string.recovery_purge), color = MaterialTheme.colorScheme.error) }
                        }
                    }
                    HorizontalDivider()
                }
            }
        }
    }
    if (confirmation != null || emptyConfirm || restoreVersion != null) {
        val restoring = restoreVersion != null
        AlertDialog(onDismissRequest = { confirmation = null; emptyConfirm = false; restoreVersion = null },
            title = { Text(stringResource(if (restoring) R.string.recovery_restore else R.string.recovery_purge)) },
            text = { Text(stringResource(if (restoring) R.string.recovery_version_hint else R.string.recovery_purge_warning)) },
            confirmButton = { TextButton(onClick = {
                val id = confirmation?.getString("id"); val version = restoreVersion
                confirmation = null; emptyConfirm = false; restoreVersion = null
                action { if (version != null && node != null) it.restoreVersion(node.id, version) else if (id != null) it.purge(id) else it.emptyTrash() }
            }) { Text(stringResource(if (restoring) R.string.recovery_restore else R.string.recovery_purge)) } },
            dismissButton = { TextButton(onClick = { confirmation = null; emptyConfirm = false; restoreVersion = null }) { Text(stringResource(R.string.cancel)) } })
    }
}
