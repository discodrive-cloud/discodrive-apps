package org.discodrive.android

import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.platform.LocalContext
import android.content.Intent
import androidx.core.content.FileProvider
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.activity.compose.BackHandler
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
fun SettingsScreen(vm: BrowserViewModel, onAutoUpload: () -> Unit, onFolderSync: () -> Unit, onBack: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember { Prefs(context) }
    var logging by remember { mutableStateOf(prefs.loggingEnabled) }
    BackHandler(onBack = onBack)
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
            Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
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
                // Pinned at pairing because the system did not trust it: shown so it can be
                // compared with the server's again.
                if (vm.serverPin.isNotEmpty()) {
                    Text(stringResource(R.string.settings_cert_pinned), style = MaterialTheme.typography.labelMedium,
                        modifier = Modifier.padding(top = 8.dp))
                    SelectionContainer {
                        Text(CertTrust.fingerprintLines(vm.serverPin), fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            HorizontalDivider()
            Column(Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable { onAutoUpload() }.padding(vertical = 8.dp)) {
                Text(stringResource(R.string.au_title), style = MaterialTheme.typography.labelLarge)
                Text(
                    stringResource(if (vm.autoUploadOn) R.string.on else R.string.off),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            HorizontalDivider()
            Column(Modifier.fillMaxWidth().heightIn(min = 48.dp).clickable { onFolderSync() }.padding(vertical = 8.dp)) {
                Text(stringResource(R.string.sync_title), style = MaterialTheme.typography.labelLarge)
                Text(
                    stringResource(if (vm.folderSyncOn) R.string.on else R.string.off),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            HorizontalDivider()
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.diagnostics_title), style = MaterialTheme.typography.labelLarge)
                Row {
                    Text(stringResource(R.string.diagnostics_enable), modifier = Modifier.weight(1f))
                    Switch(checked = logging, onCheckedChange = { logging = it; prefs.loggingEnabled = it })
                }
                Text(stringResource(R.string.diagnostics_hint), style = MaterialTheme.typography.bodySmall)
                if (Diagnostics.file(context).exists()) {
                    TextButton(onClick = {
                        val uri = FileProvider.getUriForFile(context, context.packageName + ".fileprovider", Diagnostics.file(context))
                        context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply {
                            type = "text/plain"; putExtra(Intent.EXTRA_STREAM, uri)
                            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                        }, null))
                    }) { Text(stringResource(R.string.diagnostics_export)) }
                }
            }
            HorizontalDivider()
            Button(onClick = { unpairDialog = true }) { Text(stringResource(R.string.settings_unpair)) }
            Spacer(Modifier.height(8.dp))
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
