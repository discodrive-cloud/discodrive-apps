package org.discodrive.android

import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.os.LocaleListCompat

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(vm: BrowserViewModel, onBack: () -> Unit) {
    var langMenu by remember { mutableStateOf(false) }
    var unpairDialog by remember { mutableStateOf(false) }

    // (BCP-47 tag, endonym). null tag = system default.
    val langs = listOf(
        null to stringResource(R.string.settings_language_system),
        "en" to stringResource(R.string.lang_en),
        "de" to stringResource(R.string.lang_de),
        "uk" to stringResource(R.string.lang_uk),
        "fr" to stringResource(R.string.lang_fr),
        "es" to stringResource(R.string.lang_es),
        "ru" to stringResource(R.string.lang_ru),
        "sr" to stringResource(R.string.lang_sr),
    )
    val currentTag = AppCompatDelegate.getApplicationLocales().toLanguageTags()
        .split(",").firstOrNull()?.take(2)?.ifEmpty { null }
    val currentLabel = langs.firstOrNull { it.first == currentTag }?.second ?: langs[0].second

    Scaffold(topBar = {
        TopAppBar(
            title = { Text(stringResource(R.string.settings_title)) },
            navigationIcon = {
                IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) }
            }
        )
    }) { pad ->
        Column(
            Modifier.padding(pad).fillMaxSize().padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp)
        ) {
            Column {
                Text(stringResource(R.string.settings_language), style = MaterialTheme.typography.labelLarge)
                Box {
                    Text(currentLabel, modifier = Modifier.fillMaxWidth().clickable { langMenu = true }.padding(vertical = 12.dp))
                    DropdownMenu(expanded = langMenu, onDismissRequest = { langMenu = false }) {
                        langs.forEach { (tag, label) ->
                            DropdownMenuItem(text = { Text(label) }, onClick = {
                                langMenu = false
                                AppCompatDelegate.setApplicationLocales(
                                    if (tag == null) LocaleListCompat.getEmptyLocaleList()
                                    else LocaleListCompat.forLanguageTags(tag)
                                )
                            })
                        }
                    }
                }
            }
            HorizontalDivider()
            Column {
                Text(stringResource(R.string.settings_server), style = MaterialTheme.typography.labelLarge)
                Text(vm.server, style = MaterialTheme.typography.bodyMedium)
                Text(
                    stringResource(R.string.setup_self_signed) + ": " +
                        stringResource(if (vm.insecureTLS) R.string.on else R.string.off),
                    style = MaterialTheme.typography.bodySmall
                )
            }
            HorizontalDivider()
            // Temporary: drives one auto-upload pass by hand for on-device verification.
            // Replaced by the auto-upload screen's "Upload now" in the next task.
            Column {
                Text("Auto-upload (debug)", style = MaterialTheme.typography.labelLarge)
                val status by vm.autoUploadStatus.collectAsState()
                Button(onClick = { vm.runAutoUploadNow() }) { Text("Run auto-upload now") }
                status?.let { Text(it, style = MaterialTheme.typography.bodySmall) }
            }
            HorizontalDivider()
            Button(onClick = { unpairDialog = true }) { Text(stringResource(R.string.settings_unpair)) }
            Spacer(Modifier.weight(1f))
            Text(
                stringResource(R.string.settings_version) + " " + BuildConfig.VERSION_NAME,
                style = MaterialTheme.typography.bodySmall
            )
        }
    }

    if (unpairDialog) {
        AlertDialog(
            onDismissRequest = { unpairDialog = false },
            title = { Text(stringResource(R.string.settings_unpair_confirm_title)) },
            text = { Text(stringResource(R.string.settings_unpair_confirm_body)) },
            confirmButton = {
                TextButton(onClick = { unpairDialog = false; vm.unpair(); onBack() }) { Text(stringResource(R.string.settings_unpair)) }
            },
            dismissButton = { TextButton(onClick = { unpairDialog = false }) { Text(stringResource(R.string.cancel)) } }
        )
    }
}
