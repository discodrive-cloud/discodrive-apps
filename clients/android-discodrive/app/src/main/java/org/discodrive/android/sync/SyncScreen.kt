package org.discodrive.android.sync

import androidx.compose.foundation.layout.*
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
    val ui by vm.ui.collectAsState()
    Scaffold(topBar = {
        TopAppBar(
            title = { Text(stringResource(R.string.sync_title)) },
            navigationIcon = { IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) } }
        )
    }) { pad ->
        Column(Modifier.padding(pad).fillMaxSize().padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(stringResource(R.string.sync_master), style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
                Switch(checked = ui.enabled, onCheckedChange = { vm.setEnabled(it) })
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
                    stringResource(R.string.sync_state, ui.state,
                        if (ui.lastSyncUnix > 0) DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(ui.lastSyncUnix * 1000))
                        else stringResource(R.string.sync_never)),
                    style = MaterialTheme.typography.bodySmall
                )
                ui.setAside?.let {
                    Text(stringResource(R.string.sync_set_aside, it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.secondary)
                }
                if (vm.bulkDeleteBlocked) {
                    Text(stringResource(R.string.sync_bulk_blocked), color = MaterialTheme.colorScheme.error)
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { vm.resyncFromServer() }) { Text(stringResource(R.string.sync_bulk_resync)) }
                        TextButton(onClick = { vm.confirmBulkDelete() }) { Text(stringResource(R.string.sync_bulk_confirm)) }
                    }
                } else {
                    ui.lastError?.let { Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall) }
                }
                Text(stringResource(R.string.sync_live), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.secondary)
            }
        }
    }
}
