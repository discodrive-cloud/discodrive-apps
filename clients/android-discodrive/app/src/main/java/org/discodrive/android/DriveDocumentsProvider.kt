package org.discodrive.android

import android.database.Cursor
import android.database.MatrixCursor
import android.os.CancellationSignal
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.provider.DocumentsContract.Document
import android.provider.DocumentsContract.Root
import android.provider.DocumentsProvider
import android.webkit.MimeTypeMap
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileNotFoundException
import java.security.MessageDigest

/** Read-only system picker access. Vaults remain encrypted here and open in the app. */
class DriveDocumentsProvider : DocumentsProvider() {
    override fun onCreate() = true
    private val app get() = requireNotNull(context)
    private fun account(): String {
        val prefs = Prefs(app)
        val token = prefs.deviceToken ?: throw FileNotFoundException("Not paired")
        if (prefs.unpairing) throw FileNotFoundException("Account is closing")
        return MessageDigest.getInstance("SHA-256").digest((prefs.serverURL + "\n" + token).toByteArray()).joinToString("") { "%02x".format(it) }
    }
    private fun node(documentId: String): String {
        val prefix = account() + ":"
        if (!documentId.startsWith(prefix)) throw FileNotFoundException("This document belongs to a previous pairing")
        return documentId.removePrefix(prefix)
    }
    override fun queryRoots(projection: Array<out String>?): Cursor {
        val result = MatrixCursor(projection ?: ROOT_COLUMNS)
        val key = runCatching { account() }.getOrNull() ?: return result
        add(result, mapOf(Root.COLUMN_ROOT_ID to key, Root.COLUMN_DOCUMENT_ID to "$key:", Root.COLUMN_TITLE to "DiscoDrive",
            Root.COLUMN_FLAGS to Root.FLAG_SUPPORTS_IS_CHILD, Root.COLUMN_MIME_TYPES to "*/*", Root.COLUMN_ICON to R.mipmap.ic_launcher))
        return result
    }
    override fun queryDocument(documentId: String, projection: Array<out String>?): Cursor {
        val id = node(documentId)
        val result = MatrixCursor(projection ?: DOCUMENT_COLUMNS)
        if (id.isEmpty()) addDocument(result, documentId, JSONObject().put("name", "DiscoDrive").put("isDir", true))
        else BrowserHolder.use(app) { browser -> node(documentId); addDocument(result, documentId, JSONObject(browser.document(id))) }
            ?: throw FileNotFoundException("Not paired")
        return result
    }
    override fun queryChildDocuments(parentDocumentId: String, projection: Array<out String>?, sortOrder: String?): Cursor {
        val parent = node(parentDocumentId)
        val result = MatrixCursor(projection ?: DOCUMENT_COLUMNS)
        BrowserHolder.use(app) { browser ->
            node(parentDocumentId)
            // Metadata only. Cached listings remain available if the network is offline.
            runCatching { browser.refresh() }
            val entries = JSONArray(browser.list(parent))
            val prefix = parentDocumentId.substringBefore(':') + ":"
            for (i in 0 until entries.length()) {
                val entry = entries.getJSONObject(i)
                addDocument(result, prefix + entry.getString("id"), entry)
            }
        } ?: throw FileNotFoundException("Not paired")
        result.setNotificationUri(app.contentResolver, DocumentsContract.buildChildDocumentsUri(AUTHORITY, parentDocumentId))
        return result
    }
    override fun isChildDocument(parentDocumentId: String, documentId: String): Boolean {
        val parent = node(parentDocumentId); val child = node(documentId)
        if (child.isEmpty()) return false
        return BrowserHolder.use(app) { browser ->
            node(parentDocumentId)
            val childPath = browser.relPath(child)
            val parentPath = if (parent.isEmpty()) "" else browser.relPath(parent)
            childPath.isNotEmpty() && (parent.isEmpty() || (parentPath.isNotEmpty() && childPath.startsWith("$parentPath/")))
        } ?: false
    }
    override fun openDocument(documentId: String, mode: String, signal: CancellationSignal?): ParcelFileDescriptor {
        if (mode != "r") throw FileNotFoundException("Open DiscoDrive to change this file")
        val id = node(documentId)
        if (id.isEmpty()) throw FileNotFoundException("This is a folder")
        signal?.throwIfCanceled()
        return BrowserHolder.use(app) { browser ->
            node(documentId)
            runCatching { browser.refresh() }
            val file = File(browser.download(id))
            signal?.throwIfCanceled()
            ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY)
        } ?: throw FileNotFoundException("Not paired")
    }
    private fun addDocument(cursor: MatrixCursor, id: String, entry: JSONObject) {
        val directory = entry.optBoolean("isDir")
        val name = entry.getString("name")
        val mime = if (directory) Document.MIME_TYPE_DIR else MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"
        add(cursor, mapOf(Document.COLUMN_DOCUMENT_ID to id, Document.COLUMN_DISPLAY_NAME to name, Document.COLUMN_MIME_TYPE to mime,
            Document.COLUMN_FLAGS to 0, Document.COLUMN_SIZE to if (directory) null else entry.optLong("size")))
    }
    private fun add(cursor: MatrixCursor, values: Map<String, Any?>) { cursor.addRow(cursor.columnNames.map { values[it] }.toTypedArray()) }
    companion object {
        fun notifyRoots(context: android.content.Context) {
            context.contentResolver.notifyChange(DocumentsContract.buildRootsUri(AUTHORITY), null)
        }
        const val AUTHORITY = "org.discodrive.android.documents"
        private val ROOT_COLUMNS = arrayOf(Root.COLUMN_ROOT_ID, Root.COLUMN_DOCUMENT_ID, Root.COLUMN_TITLE, Root.COLUMN_FLAGS, Root.COLUMN_MIME_TYPES, Root.COLUMN_ICON)
        private val DOCUMENT_COLUMNS = arrayOf(Document.COLUMN_DOCUMENT_ID, Document.COLUMN_DISPLAY_NAME, Document.COLUMN_MIME_TYPE, Document.COLUMN_FLAGS, Document.COLUMN_SIZE)
    }
}
