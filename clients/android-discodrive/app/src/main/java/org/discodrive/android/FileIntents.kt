package org.discodrive.android

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.webkit.MimeTypeMap
import androidx.core.content.FileProvider
import java.io.File

/**
 * Hands a downloaded file to other apps. The copy lives in app storage, so other apps get it
 * through the FileProvider with a read grant, never by path.
 */
object FileIntents {
    private const val AUTHORITY = "org.discodrive.android.fileprovider"

    fun mimeOf(name: String): String =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(File(name).extension.lowercase()) ?: "application/octet-stream"

    private fun uriOf(ctx: Context, path: String): Uri = FileProvider.getUriForFile(ctx, AUTHORITY, File(path))

    /**
     * Opens the file in the app that handles its type. An APK goes to the system installer:
     * the GitHub build may ask for it (REQUEST_INSTALL_PACKAGES, full flavor only); in the
     * Google Play build the installer refuses, and Save to device is the way.
     */
    fun open(ctx: Context, path: String) {
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uriOf(ctx, path), mimeOf(path))
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        ctx.startActivity(intent)
    }

    /** The system share sheet: a messenger, mail, a file manager's "save here". */
    fun send(ctx: Context, path: String, title: String) {
        val uri = uriOf(ctx, path)
        val intent = Intent(Intent.ACTION_SEND)
            .setType(mimeOf(path))
            .putExtra(Intent.EXTRA_STREAM, uri)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        intent.clipData = android.content.ClipData.newRawUri(File(path).name, uri)
        ctx.startActivity(Intent.createChooser(intent, title).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
}
