package org.discodrive.fastsync

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.DocumentsContract
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import java.text.DateFormat
import java.util.Date

class MainActivity : ComponentActivity() {
    private val vm: SyncViewModel by viewModels()
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { FastSyncTheme { Root(vm) } }
    }
    override fun onResume() { super.onResume(); vm.refreshAfterPermission() }
}

@Composable
fun Root(vm: SyncViewModel) {
    val ui by vm.ui.collectAsState()
    val ctx = LocalContext.current
    var hasPerm by remember { mutableStateOf(vm.hasStoragePermission()) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        hasPerm = vm.hasStoragePermission()
        vm.refreshAfterPermission()
    }
    when {
        !hasPerm -> PermissionGate {
            launcher.launch(
                Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                    Uri.parse("package:" + ctx.packageName))
            )
        }
        ui.paired -> SyncScreen(vm, ui)
        else -> SetupScreen(vm, ui)
    }
}

@Composable
private fun Screen(content: @Composable ColumnScope.() -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp),
        content = content
    )
}

@Composable
fun PermissionGate(onGrant: () -> Unit) {
    Screen {
        Spacer(Modifier.height(80.dp))
        Text("Storage access needed", style = MaterialTheme.typography.titleLarge)
        Text("Grant “All files access” so the sync folder is reachable by other apps (e.g. KeePass).")
        Button(onClick = onGrant) { Text("Grant access") }
    }
}

@Composable
fun SetupScreen(vm: SyncViewModel, ui: UiState) {
    val ctx = LocalContext.current
    var server by remember { mutableStateOf("https://") }
    var insecure by remember { mutableStateOf(false) }
    Screen {
        Spacer(Modifier.height(60.dp))
        Text("DiscoDrive Fast Sync", style = MaterialTheme.typography.titleLarge)
        OutlinedTextField(
            value = server, onValueChange = { server = it }, singleLine = true,
            label = { Text("Server URL") },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth()
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Switch(checked = insecure, onCheckedChange = { insecure = it })
            Spacer(Modifier.width(8.dp)); Text("Self-signed certificate")
        }
        if (insecure) {
            Text(
                "Disables TLS certificate checks — your token and files can be intercepted. Use only on a network you trust.",
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall
            )
        }
        if (ui.pendingUserCode != null) {
            Text("Code: ${ui.pendingUserCode}", style = MaterialTheme.typography.titleMedium)
            Text("Confirm this code in the browser to pair.")
            CircularProgressIndicator()
        } else {
            Button(
                onClick = { vm.pair(server, insecure) { url ->
                    ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
                } },
                enabled = !ui.working
            ) { Text("Pair device") }
        }
        ui.lastError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    }
}

@Composable
fun SyncScreen(vm: SyncViewModel, ui: UiState) {
    val ctx = LocalContext.current
    var confirmUnpair by remember { mutableStateOf(false) }
    Screen {
        Spacer(Modifier.height(40.dp))
        Text("DiscoDrive Fast Sync", style = MaterialTheme.typography.titleLarge)
        Text(vm.syncDir.path, style = MaterialTheme.typography.bodySmall)
        Button(onClick = { vm.syncNow() }, enabled = !ui.working, modifier = Modifier.fillMaxWidth()) {
            if (ui.working) { CircularProgressIndicator(Modifier.size(18.dp)); Spacer(Modifier.width(8.dp)) }
            Text(if (ui.working) "Syncing…" else "Sync now")
        }
        Text("State: ${ui.state} · last sync: ${lastSyncText(ui.lastSyncUnix)}",
            style = MaterialTheme.typography.bodySmall)
        ui.lastError?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        // The sync stopped rather than delete most of the vault. Offer the way out here, or
        // the only paths left would be doing nothing or unpairing.
        if (vm.bulkDeleteBlocked) {
            var confirmDelete by remember { mutableStateOf(false) }
            // Listed first, and phrased as the ordinary thing to do: an emptied folder is far
            // more often a mirror that went missing than files anyone meant to delete.
            Button(onClick = { vm.resyncFromServer() }, enabled = !ui.working) {
                Text("Download everything again")
            }
            OutlinedButton(onClick = { confirmDelete = true }) {
                Text("Delete them on the server", color = MaterialTheme.colorScheme.error)
            }
            if (confirmDelete) {
                AlertDialog(
                    onDismissRequest = { confirmDelete = false },
                    title = { Text("Delete on the server?") },
                    text = {
                        Text(
                            "These files are gone from this device's sync folder. Deleting them " +
                                "on the server removes them for every device. If the folder was " +
                                "emptied by accident — a reinstall, a folder moved or a drive not " +
                                "mounted — unpair instead and pair again."
                        )
                    },
                    confirmButton = {
                        TextButton(onClick = { confirmDelete = false; vm.confirmBulkDelete() }) {
                            Text("Delete", color = MaterialTheme.colorScheme.error)
                        }
                    },
                    dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text("Cancel") } },
                )
            }
        }
        OutlinedButton(onClick = { openFiles(ctx) }) { Text("Open folder") }
        Spacer(Modifier.weight(1f))
        TextButton(onClick = { confirmUnpair = true }) {
            Text("Unpair", color = MaterialTheme.colorScheme.error)
        }
    }
    if (confirmUnpair) {
        AlertDialog(
            onDismissRequest = { confirmUnpair = false },
            title = { Text("Unpair this device?") },
            text = { Text("You'll need to pair again to sync.") },
            confirmButton = { TextButton(onClick = { confirmUnpair = false; vm.unpair() }) { Text("Unpair") } },
            dismissButton = { TextButton(onClick = { confirmUnpair = false }) { Text("No") } }
        )
    }
}

private fun lastSyncText(unix: Long): String =
    if (unix > 0) DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(unix * 1000))
    else "never"

// Best-effort: try to open the Files/Documents UI AT our folder; fall back to primary storage
// root. Android has no guaranteed "open this exact folder" intent — behaviour varies by OEM/app.
private fun openFiles(ctx: Context) {
    val authority = "com.android.externalstorage.documents"
    val targets = listOf(
        DocumentsContract.buildDocumentUri(authority, "primary:DiscoDriveFastSync/Sync")
            to DocumentsContract.Document.MIME_TYPE_DIR,
        DocumentsContract.buildRootUri(authority, "primary")
            to "vnd.android.document/root",
    )
    for ((uri, mime) in targets) {
        try {
            ctx.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime))
            return
        } catch (_: Exception) { }
    }
}
