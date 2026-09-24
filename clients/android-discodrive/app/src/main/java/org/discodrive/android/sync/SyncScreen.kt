package org.discodrive.android.sync

import androidx.compose.foundation.layout.*
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import org.json.JSONObject
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import org.discodrive.android.R
import java.text.DateFormat
import java.util.Date

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SyncScreen(vm: SyncViewModel, onBack: () -> Unit) {
    BackHandler(onBack = onBack)
    val ui by vm.ui.collectAsState()
    var confirmDelete by remember { mutableStateOf(false) }
    val activity = remember(ui.activity) { runCatching { JSONObject(ui.activity) }.getOrDefault(JSONObject()) }
    Scaffold(topBar = {
        TopAppBar(
            title = { Text(stringResource(R.string.sync_title)) },
            navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) } }
        )
    }) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(R.string.sync_master), style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                Switch(checked = ui.enabled, enabled = !ui.changing, onCheckedChange = { vm.setEnabled(it) })
            }
            Text(stringResource(R.string.sync_desc), style = MaterialTheme.typography.bodyMedium)
            Text(stringResource(R.string.sync_two_way), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
            HorizontalDivider()
            Column {
                Text(stringResource(R.string.sync_folder), style = MaterialTheme.typography.labelLarge)
                Text(vm.syncDir.path, style = MaterialTheme.typography.bodySmall)
            }
            if (ui.enabled) {
                Button(onClick = { vm.syncNow() }, enabled = !ui.working, modifier = Modifier.fillMaxWidth()) {
                    if (ui.working) { CircularProgressIndicator(Modifier.size(18.dp)); Spacer(Modifier.width(8.dp)) }
                    Text(stringResource(if (ui.working) R.string.sync_syncing else R.string.sync_now))
                }
                Text(
                    stringResource(R.string.sync_state, stringResource(when (ui.state) {
                        "syncing" -> R.string.full_sync_syncing
                        "offline", "error" -> R.string.full_sync_error
                        else -> if (ui.lastSyncUnix > 0) R.string.full_sync_ready else R.string.activity_empty
                    }),
                        if (ui.lastSyncUnix > 0) DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(ui.lastSyncUnix * 1000))
                        else stringResource(R.string.sync_never)),
                    style = MaterialTheme.typography.bodySmall
                )
                ui.setAside?.let {
                    Text(stringResource(R.string.sync_set_aside, it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.secondary)
                }
                if (vm.bulkDeleteBlocked) {
                    Text(stringResource(R.string.sync_bulk_blocked), color = MaterialTheme.colorScheme.error)
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { vm.resyncFromServer() }) { Text(stringResource(R.string.sync_bulk_resync)) }
                        TextButton(onClick = { confirmDelete = true }) { Text(stringResource(R.string.sync_bulk_confirm)) }
                    }
                } else {
                    ui.lastError?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                }
                HorizontalDivider()
                Text(stringResource(R.string.activity_title), style = MaterialTheme.typography.titleMedium)
                val phase = when (activity.optString("phase")) {
                    "pull" -> R.string.activity_pull
                    "push" -> R.string.activity_push
                    else -> R.string.activity_scan
                }
                if (activity.optString("phase").isNotEmpty()) Text(stringResource(phase))
                if (activity.optString("path").isNotEmpty()) Text(activity.optString("path"))
                Text(stringResource(R.string.activity_completed) + ": " + activity.optLong("completed"))
                val errors = activity.optJSONArray("errors")
                if (errors != null && errors.length() > 0) {
                    Text(stringResource(R.string.activity_errors), style = MaterialTheme.typography.titleSmall)
                    for (i in errors.length() - 1 downTo 0) {
                        val entry = errors.getJSONObject(i)
                        Text(entry.optString("path"), style = MaterialTheme.typography.bodyMedium)
                        Text(entry.optString("message"), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    }
                }
                Text(stringResource(R.string.activity_hint), style = MaterialTheme.typography.bodySmall)
                Text(stringResource(R.string.sync_live), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.secondary)
            }
        }
    }
    if (confirmDelete) AlertDialog(onDismissRequest = { confirmDelete = false },
        title = { Text(stringResource(R.string.full_sync_confirm_delete)) },
        text = { Text(stringResource(R.string.full_sync_delete_warning)) },
        confirmButton = { TextButton(onClick = { confirmDelete = false; vm.confirmBulkDelete() }) { Text(stringResource(R.string.sync_bulk_confirm)) } },
        dismissButton = { TextButton(onClick = { confirmDelete = false }) { Text(stringResource(R.string.cancel)) } })
}
