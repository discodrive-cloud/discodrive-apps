package org.discodrive.android

import org.discodrive.android.autoupload.AutoUploadScreen

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp

class MainActivity : AppCompatActivity() {
    private val vm: BrowserViewModel by viewModels()
    private val vaultVm: VaultViewModel by viewModels()
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { MaterialTheme { Root(vm, vaultVm) } }
    }
    override fun onResume() { super.onResume(); vm.refreshAfterPermission() }
}

@Composable
fun Root(vm: BrowserViewModel, vaultVm: VaultViewModel) {
    val ui by vm.ui.collectAsState()
    val vaultUi by vaultVm.ui.collectAsState()
    val ctx = LocalContext.current
    var hasPerm by remember { mutableStateOf(vm.hasStoragePermission()) }
    var showSettings by remember { mutableStateOf(false) }
    var showAutoUpload by remember { mutableStateOf(false) }
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
        vaultUi.open -> VaultScreen(vaultVm, vaultUi)
        vaultUi.loading || vaultUi.error != null ->
            VaultOpeningScreen(vaultUi.loading, vaultUi.error) { vaultVm.dismissError() }
        ui.paired && showAutoUpload -> AutoUploadScreen(viewModel()) { showAutoUpload = false }
        ui.paired && showSettings -> SettingsScreen(vm, onAutoUpload = { showSettings = false; showAutoUpload = true }) { showSettings = false }
        ui.paired -> BrowserScreen(vm, ui,
            onUnlock = { root, pwd ->
                val tok = vm.token
                if (tok != null) vaultVm.open(vm.server, tok, root, pwd, vm.insecureTLS)
            },
            onSettings = { showSettings = true }
        )
        else -> SetupScreen(vm, ui)
    }
}

@Composable
fun VaultOpeningScreen(loading: Boolean, error: String?, onBack: () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Spacer(Modifier.height(80.dp))
        if (loading) {
            CircularProgressIndicator()
            Text(stringResource(R.string.vault_unlocking), style = MaterialTheme.typography.titleMedium)
        } else if (error != null) {
            Text(stringResource(R.string.vault_open_failed), style = MaterialTheme.typography.titleLarge)
            Text(error, color = MaterialTheme.colorScheme.error)
            Button(onClick = onBack) { Text(stringResource(R.string.back)) }
        }
    }
}

@Composable
fun PermissionGate(onGrant: () -> Unit) {
    Column(
        Modifier.fillMaxSize().padding(28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Spacer(Modifier.height(80.dp))
        Text(stringResource(R.string.perm_title), style = MaterialTheme.typography.titleLarge)
        Text(stringResource(R.string.perm_body))
        Button(onClick = onGrant) { Text(stringResource(R.string.perm_grant)) }
    }
}

@Composable
fun SetupScreen(vm: BrowserViewModel, ui: BrowseState) {
    val ctx = LocalContext.current
    var server by remember { mutableStateOf("https://") }
    var insecure by remember { mutableStateOf(false) }
    Column(
        Modifier.fillMaxSize().padding(28.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(16.dp)
    ) {
        Spacer(Modifier.height(60.dp))
        Text(stringResource(R.string.app_name), style = MaterialTheme.typography.titleLarge)
        OutlinedTextField(
            value = server, onValueChange = { server = it }, singleLine = true,
            label = { Text(stringResource(R.string.setup_server_url)) },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
            modifier = Modifier.fillMaxWidth()
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            Switch(checked = insecure, onCheckedChange = { insecure = it })
            Spacer(Modifier.width(8.dp)); Text(stringResource(R.string.setup_self_signed))
        }
        if (insecure) {
            Text(
                stringResource(R.string.setup_self_signed_warn),
                color = MaterialTheme.colorScheme.error,
                style = MaterialTheme.typography.bodySmall
            )
        }
        if (ui.pendingUserCode != null) {
            Text(stringResource(R.string.setup_code, ui.pendingUserCode ?: ""), style = MaterialTheme.typography.titleMedium)
            Text(stringResource(R.string.setup_confirm_code))
            CircularProgressIndicator()
        } else {
            Button(
                onClick = { vm.pair(server, insecure) { url -> ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) } },
                enabled = !ui.loading
            ) { Text(stringResource(R.string.setup_pair)) }
        }
        ui.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    }
}
