package org.discodrive.android.autoupload

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import org.discodrive.android.R

/**
 * The auto-upload screen: which folders go up, under what conditions, and what happened.
 *
 * Deliberately plain about two things the user has to trust: files are only ever copied
 * (never removed from the phone), and background timing is the system's call.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AutoUploadScreen(vm: AutoUploadViewModel, onBack: () -> Unit) {
    val state by vm.state.collectAsState()
    var picking by remember { mutableStateOf(false) }
    var showLog by remember { mutableStateOf(false) }

    Scaffold(topBar = {
        TopAppBar(
            title = { Text(stringResource(R.string.au_title)) },
            navigationIcon = {
                IconButton(onClick = onBack) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back))
                }
            },
        )
    }) { pad ->
        LazyColumn(
            Modifier.padding(pad).fillMaxSize().padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(R.string.au_master), Modifier.weight(1f),
                        style = MaterialTheme.typography.titleMedium)
                    Switch(checked = state.enabled, onCheckedChange = { vm.setEnabled(it) })
                }
                Text(stringResource(R.string.au_never_deletes), style = MaterialTheme.typography.bodySmall)
                state.blocked?.let {
                    Spacer(Modifier.height(4.dp))
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                }
                state.running?.let {
                    Spacer(Modifier.height(4.dp))
                    LinearProgressIndicator(Modifier.fillMaxWidth())
                    Text(it, style = MaterialTheme.typography.bodySmall)
                }
                Spacer(Modifier.height(8.dp))
                HorizontalDivider()
            }

            item { Text(stringResource(R.string.au_folders), style = MaterialTheme.typography.labelLarge) }

            items(state.rules, key = { it.sourcePath }) { rule ->
                RuleCard(
                    rule = rule,
                    onRemove = { vm.removeFolder(rule.sourcePath) },
                    onToggleSubfolders = { vm.setSubfolders(rule.sourcePath, it) },
                )
            }

            item {
                TextButton(onClick = { picking = true }) {
                    Icon(Icons.Default.Add, null)
                    Spacer(Modifier.width(8.dp))
                    Text(stringResource(R.string.au_add))
                }
                HorizontalDivider()
            }

            item {
                Text(stringResource(R.string.au_when), style = MaterialTheme.typography.labelLarge)
                SwitchRow(stringResource(R.string.au_wifi), state.wifiOnly) { vm.setWifiOnly(it) }
                SwitchRow(stringResource(R.string.au_charging), state.chargingOnly) { vm.setChargingOnly(it) }
                SwitchRow(stringResource(R.string.au_battery), state.requireBattery) { vm.setRequireBattery(it) }
                SwitchRow(stringResource(R.string.au_roaming), state.pauseOnRoaming) { vm.setPauseOnRoaming(it) }
                HorizontalDivider()
            }

            item {
                Text(
                    stringResource(R.string.au_stats, state.sent, state.skipped, state.deferred),
                    style = MaterialTheme.typography.bodySmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Button(onClick = { vm.uploadNow() }, enabled = state.enabled) {
                        Text(stringResource(R.string.au_upload_now))
                    }
                    TextButton(onClick = { showLog = true }) { Text(stringResource(R.string.au_log)) }
                }
                Text(stringResource(R.string.au_ios_hint), style = MaterialTheme.typography.bodySmall)
                Spacer(Modifier.height(24.dp))
            }
        }
    }

    if (picking) {
        FolderPickerDialog(
            start = vm.pickerStart(),
            onDismiss = { picking = false },
            onPick = { path -> picking = false; vm.addFolder(path) },
        )
    }
    if (showLog) {
        LogDialog(entries = vm.log(), onDismiss = { showLog = false })
    }
}

@Composable
private fun RuleCard(
    rule: Rule,
    onRemove: () -> Unit,
    onToggleSubfolders: (Boolean) -> Unit,
) {
    ElevatedCard(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(rule.sourceLabel, style = MaterialTheme.typography.titleSmall)
                    Text("→ ${rule.destLabel}", style = MaterialTheme.typography.bodySmall)
                    Text(rule.sourcePath, style = MaterialTheme.typography.bodySmall)
                    // What gets picked up is decided by the folder, not by a switch nobody
                    // wants to flip: pictures folders take media, everything else takes all.
                    Text(
                        stringResource(if (rule.mediaOnly) R.string.au_media_only else R.string.au_all_files),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                IconButton(onClick = onRemove) {
                    Icon(Icons.Default.Close, stringResource(R.string.au_remove))
                }
            }
            SwitchRow(stringResource(R.string.au_subfolders), rule.includeSubfolders, onToggleSubfolders)
        }
    }
}

@Composable
private fun SwitchRow(label: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Switch(checked = checked, onCheckedChange = onChange)
    }
}

@Composable
private fun LogDialog(entries: List<JournalEntry>, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.au_log_title)) },
        text = {
            if (entries.isEmpty()) {
                Text(stringResource(R.string.au_empty_log))
            } else {
                LazyColumn(Modifier.heightIn(max = 400.dp)) {
                    items(entries) { e ->
                        Column(Modifier.padding(vertical = 4.dp)) {
                            Text(e.path.substringAfterLast('/'), style = MaterialTheme.typography.bodyMedium)
                            Text(
                                if (e.error != null) "${e.state} · ${e.error}" else e.state,
                                style = MaterialTheme.typography.bodySmall,
                            )
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_ok)) } },
    )
}
