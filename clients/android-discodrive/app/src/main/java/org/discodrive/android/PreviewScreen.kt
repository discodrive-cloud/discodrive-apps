package org.discodrive.android

import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import android.webkit.MimeTypeMap
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

private data class PreviewContent(val path: String, val image: Bitmap? = null, val text: String? = null, val pages: Int = 0)
private fun decodePreview(path: String, page: Int): PreviewContent {
    val file = File(path)
    val ext = file.extension.lowercase()
    if (ext == "pdf") {
        ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY).use { descriptor ->
            PdfRenderer(descriptor).use { pdf ->
                pdf.openPage(page.coerceIn(0, pdf.pageCount - 1)).use { sheet ->
                    val scale = minOf(2f, 2048f / maxOf(sheet.width, sheet.height))
                    val bitmap = Bitmap.createBitmap(maxOf(1, (sheet.width * scale).toInt()), maxOf(1, (sheet.height * scale).toInt()), Bitmap.Config.ARGB_8888)
                    bitmap.eraseColor(android.graphics.Color.WHITE)
                    sheet.render(bitmap, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    return PreviewContent(path, image = bitmap, pages = pdf.pageCount)
                }
            }
        }
    }
    val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    BitmapFactory.decodeFile(path, options)
    if (options.outWidth > 0 && options.outHeight > 0) {
        options.inJustDecodeBounds = false
        options.inSampleSize = 1
        while (maxOf(options.outWidth, options.outHeight) / options.inSampleSize > 2048) options.inSampleSize *= 2
        return PreviewContent(path, image = BitmapFactory.decodeFile(path, options))
    }
    if (ext in setOf("txt", "md", "json", "csv", "log", "xml", "yaml", "yml", "ics", "vcf") && file.length() <= 256 * 1024) {
        return PreviewContent(path, text = file.readText())
    }
    return PreviewContent(path)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PreviewScreen(names: List<String>, initial: Int, load: suspend (Int) -> String, onClose: () -> Unit) {
    var index by remember { mutableStateOf(initial) }
    var page by remember { mutableStateOf(0) }
    var path by remember { mutableStateOf<String?>(null) }
    var content by remember { mutableStateOf<PreviewContent?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val context = LocalContext.current
    BackHandler(onBack = onClose)
    LaunchedEffect(index) {
        busy = true; path = null; content = null; error = null; page = 0
        try { path = load(index) } catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message }
        finally { busy = false }
    }
    LaunchedEffect(path, page) {
        val local = path ?: return@LaunchedEffect
        busy = true; error = null
        try { content = withContext(Dispatchers.IO) { decodePreview(local, page) } }
        catch (e: Exception) { if (e is kotlinx.coroutines.CancellationException) throw e; error = e.message }
        finally { busy = false }
    }
    Scaffold(topBar = { TopAppBar(title = { Text(names[index], maxLines = 2) }, actions = {
        TextButton(onClick = onClose) { Text(stringResource(R.string.preview_close)) }
    }) }, bottomBar = {
        Row(Modifier.fillMaxWidth().navigationBarsPadding().padding(8.dp), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
            IconButton(enabled = index > 0, onClick = { index-- }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.preview_previous)) }
            Text("${index + 1} / ${names.size}")
            IconButton(enabled = index < names.lastIndex, onClick = { index++ }) { Icon(Icons.AutoMirrored.Filled.ArrowForward, stringResource(R.string.preview_next)) }
        }
    }) { padding ->
        Column(Modifier.padding(padding).fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
            if (busy) LinearProgressIndicator(Modifier.fillMaxWidth())
            error?.let { Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp)) }
            content?.let { preview ->
                if (preview.pages > 1) Row(verticalAlignment = Alignment.CenterVertically) {
                    IconButton(enabled = !busy && page > 0, onClick = { page-- }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, stringResource(R.string.preview_previous)) }
                    Text("${page + 1} / ${preview.pages}")
                    IconButton(enabled = !busy && page < preview.pages - 1, onClick = { page++ }) { Icon(Icons.AutoMirrored.Filled.ArrowForward, stringResource(R.string.preview_next)) }
                }
                preview.image?.let { Image(it.asImageBitmap(), names[index], Modifier.fillMaxWidth().weight(1f), contentScale = ContentScale.Fit) }
                preview.text?.let { Text(it, Modifier.weight(1f).fillMaxWidth().verticalScroll(rememberScrollState()).padding(16.dp)) }
                TextButton(onClick = {
                    val uri = FileProvider.getUriForFile(context, context.packageName + ".fileprovider", File(preview.path))
                    val mime = MimeTypeMap.getSingleton().getMimeTypeFromExtension(File(preview.path).extension.lowercase()) ?: "application/octet-stream"
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, mime).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)) }
                        .onFailure { error = it.message }
                }) { Text(stringResource(R.string.action_open)) }
            }
        }
    }
}
