package org.discodrive.android

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp

/** Asks whether to trust a certificate the system rejected; the user compares the fingerprint. */
@Composable
fun CertTrustDialog(cert: CertDetails, onTrust: () -> Unit, onCancel: () -> Unit) {
    AlertDialog(
        onDismissRequest = onCancel,
        title = { Text(stringResource(R.string.cert_title)) },
        text = {
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                if (cert.selfSigned) {
                    Surface(color = MaterialTheme.colorScheme.errorContainer, shape = MaterialTheme.shapes.small) {
                        Text(
                            stringResource(R.string.cert_self_signed),
                            color = MaterialTheme.colorScheme.onErrorContainer,
                            style = MaterialTheme.typography.labelMedium,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                        )
                    }
                }
                CertField(stringResource(R.string.cert_host), cert.host)
                Column {
                    Text(stringResource(R.string.cert_fingerprint), style = MaterialTheme.typography.labelMedium)
                    SelectionContainer {
                        Text(
                            CertTrust.fingerprintLines(cert.fingerprint),
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
                CertField(stringResource(R.string.cert_subject), cert.subject)
                CertField(stringResource(R.string.cert_issuer), cert.issuer)
                CertField(stringResource(R.string.cert_expires), CertTrust.expiryDate(cert.notAfter))
                Text(stringResource(R.string.cert_hint), color = MaterialTheme.colorScheme.error)
            }
        },
        confirmButton = { TextButton(onClick = onTrust) { Text(stringResource(R.string.cert_trust)) } },
        dismissButton = { TextButton(onClick = onCancel) { Text(stringResource(R.string.cancel)) } },
    )
}

@Composable
private fun CertField(label: String, value: String) {
    if (value.isEmpty()) return
    Column {
        Text(label, style = MaterialTheme.typography.labelMedium)
        Text(value, style = MaterialTheme.typography.bodyMedium)
    }
}

/** An error as the user reads it: a changed server certificate is explained, not just quoted. */
@Composable
fun explainError(message: String): String =
    CertTrust.describe(message, stringResource(R.string.cert_changed)) ?: message
