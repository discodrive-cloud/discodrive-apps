package org.discodrive.android

import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SharingScreen(node: Entry, onBack: () -> Unit) {
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    val scope = rememberCoroutineScope()
    var byLink by remember { mutableStateOf(true) }
    var email by remember { mutableStateOf("") }
    var days by remember { mutableStateOf(7) }
    var expiryMenu by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var mutating by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var link by remember { mutableStateOf<String?>(null) }
    var createdID by remember { mutableStateOf<String?>(null) }
    var shares by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    val expiry = listOf(0 to R.string.share_forever, 1 to R.string.share_day, 7 to R.string.share_week, 30 to R.string.share_month)
    suspend fun load() {
        val json = withContext(Dispatchers.IO) { BrowserHolder.use(context) { it.shares(node.id) } ?: kotlin.error("Not paired") }
        val array = JSONArray(json)
        // A row without its id could be neither shown nor revoked; drop it rather than crash.
        shares = List(array.length()) { array.optJSONObject(it) }.filterNotNull()
            .filter { it.optString("share_id").isNotEmpty() }
    }
    fun run(block: (mobile.Browser) -> String?, revoked: String? = null) {
        if (busy) return
        scope.launch {
            busy = true; mutating = true; error = null
            try {
                val result = withContext(Dispatchers.IO) { BrowserHolder.use(context) { browser -> Pair(true, block(browser)) } ?: kotlin.error("Not paired") }
                result.second?.let { val json = JSONObject(it); link = json.optString("url").takeIf { url -> url.isNotEmpty() }; createdID = json.optString("share_id").ifEmpty { null } }
                if (revoked != null && revoked == createdID) { link = null; createdID = null }
                load()
            } catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message }
            finally { busy = false; mutating = false }
        }
    }
    LaunchedEffect(node.id) { busy = true; try { load() } catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message } finally { busy = false } }
    BackHandler { if (!mutating) onBack() }
    Scaffold(topBar = { TopAppBar(title = { Text(stringResource(R.string.share_title)) }, navigationIcon = {
        IconButton(onClick = onBack, enabled = !mutating) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.cd_back)) }
    }) }) { padding ->
        Column(Modifier.padding(padding).fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(node.name, style = MaterialTheme.typography.titleMedium)
            Row {
                FilterChip(selected = byLink, enabled = !busy, onClick = { byLink = true }, label = { Text(stringResource(R.string.share_link)) })
                Spacer(Modifier.width(8.dp))
                FilterChip(selected = !byLink, enabled = !busy, onClick = { byLink = false }, label = { Text(stringResource(R.string.share_user)) })
            }
            if (!byLink) OutlinedTextField(value = email, onValueChange = { email = it }, label = { Text(stringResource(R.string.share_email)) }, enabled = !busy, singleLine = true, modifier = Modifier.fillMaxWidth())
            Box {
                OutlinedButton(onClick = { expiryMenu = true }, enabled = !busy) { Text(stringResource(R.string.share_expiry) + ": " + stringResource(expiry.first { it.first == days }.second)) }
                DropdownMenu(expanded = expiryMenu, onDismissRequest = { expiryMenu = false }) {
                    expiry.forEach { (value, label) -> DropdownMenuItem(text = { Text(stringResource(label)) }, onClick = { days = value; expiryMenu = false }) }
                }
            }
            Text(stringResource(R.string.share_read_only), style = MaterialTheme.typography.bodySmall)
            Button(enabled = !busy && (byLink || email.isNotBlank()), onClick = { val recipient = if (byLink) "" else email.trim(); val lifetime = days; run({ it.createShare(node.id, recipient, lifetime.toLong()) }) }) { Text(stringResource(if (byLink) R.string.share_create else R.string.share_grant)) }
            link?.let { url ->
                Row {
                    TextButton(onClick = { clipboard.setText(AnnotatedString(url)) }) { Text(stringResource(R.string.share_copy)) }
                    TextButton(onClick = { context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).apply { type = "text/plain"; putExtra(Intent.EXTRA_TEXT, url) }, null)) }) { Text(stringResource(R.string.share_send)) }
                }
            }
            HorizontalDivider()
            Text(stringResource(R.string.share_existing), style = MaterialTheme.typography.titleMedium)
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            if (!busy && shares.isEmpty() && error == null) Text(stringResource(R.string.share_empty))
            shares.forEach { share ->
                val id = share.getString("share_id")
                Text(share.optString("email").takeUnless { it.isBlank() || it == "null" } ?: (stringResource(R.string.share_link) + " · " + id.take(8)))
                val expiryDate = share.optString("expires_at").takeUnless { it.isBlank() || it == "null" }
                if (expiryDate == null) Text(stringResource(R.string.share_forever), style = MaterialTheme.typography.bodySmall)
                else {
                    val display = runCatching {
                        java.text.DateFormat.getDateTimeInstance(java.text.DateFormat.MEDIUM, java.text.DateFormat.SHORT)
                            .format(java.util.Date.from(java.time.Instant.parse(expiryDate)))
                    }.getOrDefault(expiryDate)
                    Text(stringResource(R.string.share_expires_at) + ": " + display, style = MaterialTheme.typography.bodySmall)
                }
                TextButton(enabled = !busy, onClick = { run({ it.revokeShare(id); null }, id) }) { Text(stringResource(R.string.share_revoke), color = MaterialTheme.colorScheme.error) }
                HorizontalDivider()
            }
        }
    }
}
