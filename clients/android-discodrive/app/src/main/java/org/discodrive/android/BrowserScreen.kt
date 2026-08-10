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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import java.io.File

private const val AUTHORITY = "org.discodrive.android.fileprovider"

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BrowserScreen(vm: BrowserViewModel, ui: BrowseState, onUnlock: (String, String) -> Unit, onSettings: () -> Unit) {
    val ctx = LocalContext.current
    var sheetFor by remember { mutableStateOf<Entry?>(null) }
    var renameFor by remember { mutableStateOf<Entry?>(null) }
    var moveFor by remember { mutableStateOf<Entry?>(null) }
    var newFolder by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf(false) }
    var unlockDialog by remember { mutableStateOf(false) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri ->
        if (uri != null) vm.uploadUri(uri)
    }

    BackHandler(enabled = !vm.atRoot()) { vm.back() }

    Scaffold(topBar = {
        TopAppBar(
            title = { Text(ui.stack.last().name) },
            navigationIcon = {
                if (!vm.atRoot()) IconButton(onClick = { vm.back() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) }
            },
            actions = {
                if (vm.currentFolderIsVault()) {
                    IconButton(onClick = { unlockDialog = true }) { Icon(Icons.Default.Lock, stringResource(R.string.cd_unlock)) }
                }
                // Pulls from the server, not just a relist of the index: this is also how the
                // user retries after a pull that failed while the app was in the background.
                IconButton(onClick = { vm.syncNow() }, enabled = !ui.syncing) {
                    Icon(Icons.Default.Refresh, stringResource(R.string.cd_refresh))
                }
                IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, stringResource(R.string.cd_menu)) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_new_folder)) }, onClick = { menu = false; newFolder = true })
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_upload)) }, onClick = { menu = false; picker.launch("*/*") })
                    DropdownMenuItem(text = { Text(stringResource(R.string.menu_settings)) }, onClick = { menu = false; onSettings() })
                }
            }
        )
    }) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            if (ui.loading || ui.syncing) LinearProgressIndicator(Modifier.fillMaxWidth())
            ui.error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(12.dp)) }
            // The first pull after pairing has nothing in the index to show yet. A bare empty
            // list read as a broken app, so say what the progress bar is for.
            if (ui.syncing && ui.entries.isEmpty() && ui.error == null) {
                Text(
                    stringResource(R.string.browse_first_sync),
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.fillMaxWidth().padding(24.dp),
                    textAlign = TextAlign.Center,
                )
            }
            LazyColumn(Modifier.fillMaxSize()) {
                items(ui.entries, key = { it.id }) { e ->
                    ListItem(
                        headlineContent = { Text(e.name) },
                        supportingContent = {
                            if (!e.isDir) {
                                val s = when {
                                    e.stale && e.cached -> stringResource(R.string.status_stale)
                                    e.pinned -> stringResource(R.string.status_pinned)
                                    e.cached -> stringResource(R.string.status_cached)
                                    else -> ""
                                }
                                if (s.isNotEmpty()) Text(s, style = MaterialTheme.typography.bodySmall)
                            }
                        },
                        leadingContent = {
                            Icon(if (e.isDir) Icons.Default.Folder else Icons.AutoMirrored.Filled.InsertDriveFile, null)
                        },
                        trailingContent = {
                            IconButton(onClick = { sheetFor = e }) { Icon(Icons.Default.MoreVert, stringResource(R.string.cd_actions)) }
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
                if (!e.isDir) {
                    sheetItem(stringResource(R.string.action_open)) { sheetFor = null; vm.open(e.id) { path -> openInApp(ctx, path) } }
                    sheetItem(stringResource(R.string.action_download)) { sheetFor = null; vm.download(e.id) }
                    if (e.pinned) sheetItem(stringResource(R.string.action_unpin)) { sheetFor = null; vm.unpin(e.id) }
                    else sheetItem(stringResource(R.string.action_pin)) { sheetFor = null; vm.pin(e.id) }
                    if (e.cached) sheetItem(stringResource(R.string.action_remove_local)) { sheetFor = null; vm.removeLocal(e.id) }
                }
                sheetItem(stringResource(R.string.action_rename)) { sheetFor = null; renameFor = e }
                sheetItem(stringResource(R.string.action_move)) { sheetFor = null; moveFor = e }
                sheetItem(stringResource(R.string.action_delete)) { sheetFor = null; vm.delete(e.id) }
            }
        }
    }

    if (newFolder) {
        TextDialog(stringResource(R.string.dialog_new_folder_title), stringResource(R.string.name), "") { name ->
            newFolder = false
            if (name != null && name.isNotBlank()) vm.mkdir(name)
        }
    }
    renameFor?.let { e ->
        TextDialog(stringResource(R.string.dialog_rename_title), stringResource(R.string.dialog_new_name), e.name) { name ->
            renameFor = null
            if (name != null && name.isNotBlank()) vm.rename(e.id, name)
        }
    }
    moveFor?.let { e ->
        MovePicker(vm, moving = e, onDismiss = { moveFor = null }) { destId ->
            moveFor = null
            vm.move(e.id, destId)
        }
    }
    if (unlockDialog) {
        PasswordDialog(stringResource(R.string.unlock_vault)) { pwd ->
            unlockDialog = false
            if (pwd != null && pwd.isNotEmpty()) onUnlock(vm.currentRelPath(), pwd)
        }
    }
}

@Composable
private fun PasswordDialog(title: String, onResult: (String?) -> Unit) {
    var pwd by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = { onResult(null) },
        title = { Text(title) },
        text = {
            OutlinedTextField(
                value = pwd, onValueChange = { pwd = it }, singleLine = true,
                label = { Text(stringResource(R.string.password)) },
                visualTransformation = PasswordVisualTransformation(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password)
            )
        },
        confirmButton = { TextButton(onClick = { onResult(pwd) }) { Text(stringResource(R.string.unlock)) } },
        dismissButton = { TextButton(onClick = { onResult(null) }) { Text(stringResource(R.string.cancel)) } }
    )
}

@Composable
private fun sheetItem(text: String, onClick: () -> Unit) {
    Text(
        text,
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 24.dp, vertical = 14.dp)
    )
}

@Composable
private fun TextDialog(title: String, label: String, initial: String, onResult: (String?) -> Unit) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = { onResult(null) },
        title = { Text(title) },
        text = { OutlinedTextField(value = text, onValueChange = { text = it }, singleLine = true, label = { Text(label) }) },
        confirmButton = { TextButton(onClick = { onResult(text) }) { Text(stringResource(R.string.ok)) } },
        dismissButton = { TextButton(onClick = { onResult(null) }) { Text(stringResource(R.string.cancel)) } }
    )
}

// Move picker: navigate folders only; "Move here" picks the current folder as destination.
// The moved item itself is filtered out so you can't move a folder into itself.
@Composable
private fun MovePicker(vm: BrowserViewModel, moving: Entry, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    var stack by remember { mutableStateOf(listOf(Folder("", ""))) }
    var folders by remember { mutableStateOf<List<Entry>>(emptyList()) }
    LaunchedEffect(stack) { folders = vm.listFolders(stack.last().id).filter { it.id != moving.id } }
    val rootName = stringResource(R.string.app_name)
    val title = if (stack.size > 1) stack.last().name else rootName
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.move_to, title)) },
        text = {
            Column {
                if (stack.size > 1) {
                    Text(stringResource(R.string.move_up), modifier = Modifier.fillMaxWidth().clickable { stack = stack.dropLast(1) }.padding(8.dp))
                }
                folders.forEach { f ->
                    Text("📁 ${f.name}", modifier = Modifier.fillMaxWidth().clickable { stack = stack + Folder(f.id, f.name) }.padding(8.dp))
                }
            }
        },
        confirmButton = { TextButton(onClick = { onPick(stack.last().id) }) { Text(stringResource(R.string.move_here)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } }
    )
}

private fun openInApp(ctx: Context, path: String) {
    val file = File(path)
    val uri = FileProvider.getUriForFile(ctx, AUTHORITY, file)
    val ext = file.extension.lowercase()
    val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext) ?: "*/*"
    val intent = Intent(Intent.ACTION_VIEW).apply {
        setDataAndType(uri, mime)
        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    }
    try { ctx.startActivity(intent) } catch (_: Exception) { }
}
