package org.discodrive.android

import android.content.Context
import android.content.Intent
import android.webkit.MimeTypeMap
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import java.io.File

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VaultScreen(vm: VaultViewModel, ui: VaultState) {
    val ctx = LocalContext.current
    var sheetFor by remember { mutableStateOf<VEntry?>(null) }
    var newFolder by remember { mutableStateOf(false) }
    var previewIndex by remember { mutableStateOf<Int?>(null) }
    var previewFiles by remember { mutableStateOf<List<VEntry>>(emptyList()) }
    previewIndex?.let { selected ->
        PreviewScreen(previewFiles.map { it.name }, selected, load = { i -> vm.preview(previewFiles[i]) }) { previewIndex = null }
        return
    }

    var menu by remember { mutableStateOf(false) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        if (uri != null) vm.uploadUri(uri)
    }

    BackHandler(enabled = true) { if (vm.atRoot()) vm.lock() else vm.back() }

    Scaffold(topBar = {
        TopAppBar(
            title = { Text(ui.stack.last().name) },
            navigationIcon = {
                if (!vm.atRoot()) IconButton(onClick = { vm.back() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) }
                else IconButton(onClick = { vm.lock() }) { Icon(Icons.Default.Lock, stringResource(R.string.cd_lock)) }
            },
            actions = {
                IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, stringResource(R.string.cd_menu)) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_new_folder)) }, onClick = { menu = false; newFolder = true })
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_upload)) }, onClick = { menu = false; picker.launch("*/*") })
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_lock_vault)) }, onClick = { menu = false; vm.lock() })
                }
            }
        )
    }) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            if (ui.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
            ui.error?.let { Text(explainError(it), color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(12.dp)) }
            LazyColumn(Modifier.fillMaxSize()) {
                items(ui.entries, key = { it.fileStoragePath + it.dirID + it.name }) { e ->
                    ListItem(
                        headlineContent = { Text(e.name) },
                        leadingContent = {
                            Icon(if (e.isDir) Icons.Default.Folder else Icons.AutoMirrored.Filled.InsertDriveFile, null)
                        },
                        trailingContent = {
                            if (!e.isDir) IconButton(onClick = { sheetFor = e }) { Icon(Icons.Default.MoreVert, stringResource(R.string.cd_actions)) }
                        },
                        modifier = Modifier.clickable { if (e.isDir) vm.enter(e) else sheetFor = e }
                    )
                    HorizontalDivider()
                }
            }
        }
    }

    sheetFor?.let { e ->
        ModalBottomSheet(onDismissRequest = { sheetFor = null }) {
            Column(Modifier.padding(bottom = 24.dp)) {
                vSheetItem(stringResource(R.string.action_open)) { sheetFor = null; previewFiles = ui.entries.filter { !it.isDir }; previewIndex = previewFiles.indexOf(e).takeIf { it >= 0 } }
                vSheetItem(stringResource(R.string.action_delete)) { sheetFor = null; vm.delete(e) }
            }
        }
    }

    if (newFolder) {
        VTextDialog(stringResource(R.string.dialog_new_folder_title), stringResource(R.string.name)) { name ->
            newFolder = false
            if (name != null && name.isNotBlank()) vm.mkdir(name)
        }
    }
}

@Composable
private fun vSheetItem(text: String, onClick: () -> Unit) {
    Text(text, modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 24.dp, vertical = 14.dp))
}

@Composable
private fun VTextDialog(title: String, label: String, onResult: (String?) -> Unit) {
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = { onResult(null) },
        title = { Text(title) },
        text = { OutlinedTextField(value = text, onValueChange = { text = it }, singleLine = true, label = { Text(label) }) },
        confirmButton = { TextButton(onClick = { onResult(text) }) { Text(stringResource(R.string.ok)) } },
        dismissButton = { TextButton(onClick = { onResult(null) }) { Text(stringResource(R.string.cancel)) } }
    )
}

private const val VAULT_AUTHORITY = "org.discodrive.android.fileprovider"

private fun openVaultFile(ctx: Context, path: String) {
    val file = File(path)
    val uri = FileProvider.getUriForFile(ctx, VAULT_AUTHORITY, file)
    val ext = file.extension.lowercase()
    val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "*/*"
    val intent = Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(uri, mime)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    try { ctx.startActivity(intent) } catch (_: Exception) { }
}
