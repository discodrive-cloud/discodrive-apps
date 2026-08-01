package org.discodrive.android.autoupload

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import org.discodrive.android.R
import java.io.File

/**
 * Picks a folder on the phone.
 *
 * A plain File browser rather than the system document picker: the app holds All Files
 * Access and the Go uploader reads real paths, while the document picker hands back a
 * content:// tree URI that the native layer cannot open.
 */
@Composable
fun FolderPickerDialog(start: File, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    var current by remember { mutableStateOf(start) }
    val children by remember(current) {
        mutableStateOf(
            current.listFiles()
                ?.filter { it.isDirectory && !it.name.startsWith(".") }
                ?.sortedBy { it.name.lowercase() }
                ?: emptyList()
        )
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Row(verticalAlignment = Alignment.CenterVertically) {
                val parent = current.parentFile
                if (parent != null && parent.canRead()) {
                    IconButton(onClick = { current = parent }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back))
                    }
                }
                Column {
                    Text(stringResource(R.string.au_pick_folder), style = MaterialTheme.typography.titleSmall)
                    Text(current.path, style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        text = {
            LazyColumn(Modifier.heightIn(max = 380.dp)) {
                items(children, key = { it.path }) { dir ->
                    Row(
                        Modifier.fillMaxWidth().clickable { current = dir }.padding(vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(Icons.Default.Folder, null)
                        Spacer(Modifier.width(12.dp))
                        Text(dir.name)
                    }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = { onPick(current.path) }) { Text(stringResource(R.string.au_add)) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}
