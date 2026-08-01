package org.discodrive.android.autoupload

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import java.io.File

/** What happened to a file the journal knows about. */
const val STATE_SENT = "sent"
const val STATE_SKIPPED = "skipped-preexisting"
const val STATE_DEFERRED = "deferred"

data class JournalEntry(
    val path: String,
    val serverName: String,
    val state: String,
    val error: String?,
    val at: Long,
)

data class JournalCounts(val sent: Int, val skipped: Int, val deferred: Int)

/**
 * Remembers which local files have already been dealt with, so auto-upload never sends the
 * same photo twice.
 *
 * This is the source of truth for "was it uploaded" — deliberately NOT a diff against the
 * server. If a file were re-sent whenever it went missing on the server, deleting a photo
 * in the web UI would just make the phone put it back.
 *
 * Identity is (path, size, mtime): a file edited in place gets a new mtime and is treated
 * as new content, which is what the user means by "I changed this photo".
 */
class UploadJournal(context: Context) {

    private val helper = object : SQLiteOpenHelper(context, "autoupload.db", null, 1) {
        override fun onCreate(db: SQLiteDatabase) {
            db.execSQL(
                """
                CREATE TABLE sent(
                  path TEXT PRIMARY KEY,
                  size INTEGER NOT NULL,
                  mtime INTEGER NOT NULL,
                  sha TEXT,
                  server_name TEXT,
                  state TEXT NOT NULL,
                  attempts INTEGER NOT NULL DEFAULT 0,
                  error TEXT,
                  at INTEGER NOT NULL
                )
                """.trimIndent()
            )
            db.execSQL("CREATE INDEX sent_state ON sent(state)")
        }

        override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) = Unit
    }

    /**
     * True when this exact file (same path, size and mtime) was already handled and should
     * not be uploaded again. A deferred file is NOT known: it is meant to be retried.
     */
    fun isKnown(file: File): Boolean = helper.readableDatabase.rawQuery(
        "SELECT state FROM sent WHERE path=? AND size=? AND mtime=?",
        arrayOf(key(file), file.length().toString(), file.lastModified().toString()),
    ).use { c -> c.moveToFirst() && c.getString(0) != STATE_DEFERRED }

    /** How many attempts this file has already cost, for the give-up rule. */
    fun attempts(file: File): Int = helper.readableDatabase.rawQuery(
        "SELECT attempts FROM sent WHERE path=?", arrayOf(key(file)),
    ).use { c -> if (c.moveToFirst()) c.getInt(0) else 0 }

    fun markSent(file: File, serverName: String, sha: String?) =
        put(file, serverName, sha, STATE_SENT, error = null, attempts = 0)

    fun markDeferred(file: File, error: String) =
        put(file, serverName = null, sha = null, state = STATE_DEFERRED, error = error, attempts = attempts(file) + 1)

    /**
     * Records everything currently in the source folder as pre-existing, without uploading
     * it. This is what makes "new files only" hold when a rule is switched on over a folder
     * that already holds an archive.
     */
    fun seedPreexisting(files: List<File>) {
        val db = helper.writableDatabase
        db.beginTransaction()
        try {
            for (f in files) put(f, serverName = null, sha = null, state = STATE_SKIPPED, error = null, attempts = 0, db = db)
            db.setTransactionSuccessful()
        } finally {
            db.endTransaction()
        }
    }

    fun counts(): JournalCounts {
        fun n(state: String) = helper.readableDatabase.rawQuery(
            "SELECT COUNT(*) FROM sent WHERE state=?", arrayOf(state),
        ).use { c -> if (c.moveToFirst()) c.getInt(0) else 0 }
        return JournalCounts(sent = n(STATE_SENT), skipped = n(STATE_SKIPPED), deferred = n(STATE_DEFERRED))
    }

    fun recent(limit: Int): List<JournalEntry> = helper.readableDatabase.rawQuery(
        "SELECT path, server_name, state, error, at FROM sent ORDER BY at DESC LIMIT ?",
        arrayOf(limit.toString()),
    ).use { c ->
        buildList {
            while (c.moveToNext()) {
                add(JournalEntry(c.getString(0), c.getString(1) ?: "", c.getString(2), c.getString(3), c.getLong(4)))
            }
        }
    }

    fun close() = helper.close()

    /** Journal key: the same folder reachable as /sdcard/... and /storage/emulated/0/...
     *  must not produce two entries for one file. */
    private fun key(file: File): String = Rule.normalize(file.path)

    private fun put(
        file: File,
        serverName: String?,
        sha: String?,
        state: String,
        error: String?,
        attempts: Int,
        db: SQLiteDatabase = helper.writableDatabase,
    ) {
        val v = ContentValues().apply {
            put("path", key(file))
            put("size", file.length())
            put("mtime", file.lastModified())
            put("sha", sha)
            put("server_name", serverName)
            put("state", state)
            put("attempts", attempts)
            put("error", error)
            put("at", System.currentTimeMillis())
        }
        db.insertWithOnConflict("sent", null, v, SQLiteDatabase.CONFLICT_REPLACE)
    }
}
