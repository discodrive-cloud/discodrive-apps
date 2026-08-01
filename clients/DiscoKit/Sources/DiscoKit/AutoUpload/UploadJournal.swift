import Foundation
import GRDB

/// What happened to an asset the journal knows about.
public enum UploadState: String, Sendable {
    case sent
    /// Was already in the library when auto-upload was switched on.
    case skippedPreexisting = "skipped-preexisting"
    /// Failed; will be retried on a later pass until the attempt cap.
    case deferred
}

public struct JournalEntry: Sendable {
    public let assetID: String
    public let serverName: String
    public let state: UploadState
    public let error: String?
    public let at: Date

    public init(assetID: String, serverName: String, state: UploadState, error: String?, at: Date) {
        self.assetID = assetID; self.serverName = serverName
        self.state = state; self.error = error; self.at = at
    }
}

public struct JournalCounts: Sendable {
    public let sent: Int
    public let skipped: Int
    public let deferred: Int

    public init(sent: Int, skipped: Int, deferred: Int) {
        self.sent = sent; self.skipped = skipped; self.deferred = deferred
    }
}

/// Remembers which photos have already been dealt with, so auto-upload never sends the same
/// one twice.
///
/// This is the source of truth for "was it uploaded" — deliberately NOT a diff against the
/// server. If a photo were re-sent whenever it went missing on the server, deleting it in
/// the web UI would just make the phone put it back.
///
/// Identity is the asset's local identifier plus its modification date: editing a photo in
/// Photos bumps that date, and the edited version is genuinely new content.
public final class UploadJournal: @unchecked Sendable {   // dbQueue (GRDB) is internally synchronized
    private let dbQueue: DatabaseQueue

    public init(dbQueue: DatabaseQueue) throws {
        self.dbQueue = dbQueue
        try migrate()
    }

    public convenience init(path: String) throws {
        try self.init(dbQueue: try DatabaseQueue(path: path))
    }

    private func migrate() throws {
        try dbQueue.write { db in
            try db.execute(sql: """
                CREATE TABLE IF NOT EXISTS uploads(
                  asset_id TEXT PRIMARY KEY,
                  modified INTEGER NOT NULL,
                  bytes INTEGER NOT NULL DEFAULT 0,
                  sha TEXT,
                  server_name TEXT,
                  state TEXT NOT NULL,
                  attempts INTEGER NOT NULL DEFAULT 0,
                  error TEXT,
                  at INTEGER NOT NULL
                );
                CREATE INDEX IF NOT EXISTS idx_uploads_state ON uploads(state);
            """)
        }
    }

    /// True when this exact version of the asset was already handled. A deferred asset is
    /// NOT known: it is meant to be retried.
    public func isKnown(assetID: String, modified: Date) throws -> Bool {
        try dbQueue.read { db in
            let row = try Row.fetchOne(db, sql: """
                SELECT state FROM uploads WHERE asset_id = ? AND modified = ?
            """, arguments: [assetID, Int64(modified.timeIntervalSince1970 * 1000)])
            guard let row else { return false }
            return (row["state"] as String?) != UploadState.deferred.rawValue
        }
    }

    public func attempts(assetID: String) throws -> Int {
        try dbQueue.read { db in
            try Int.fetchOne(db, sql: "SELECT attempts FROM uploads WHERE asset_id = ?",
                             arguments: [assetID]) ?? 0
        }
    }

    /// Records an upload. `modified` must be the value the upload actually read, not
    /// today's: an asset edited mid-upload would otherwise be stored under its NEW identity
    /// and the edit would never be sent.
    public func markSent(assetID: String, modified: Date, bytes: Int64,
                         sha: String?, serverName: String) throws {
        try put(assetID: assetID, modified: modified, bytes: bytes, sha: sha,
                serverName: serverName, state: .sent, error: nil, attempts: 0)
    }

    public func markDeferred(assetID: String, modified: Date, error: String) throws {
        let n = try attempts(assetID: assetID) + 1
        try put(assetID: assetID, modified: modified, bytes: 0, sha: nil,
                serverName: nil, state: .deferred, error: error, attempts: n)
    }

    /// Records what is already in the library without uploading it. This is what makes
    /// "new photos only" hold when the feature is switched on over years of pictures.
    public func seedPreexisting(_ assets: [(id: String, modified: Date)]) throws {
        try dbQueue.write { db in
            for a in assets {
                try db.execute(sql: """
                    INSERT INTO uploads(asset_id, modified, bytes, sha, server_name, state, attempts, error, at)
                    VALUES (?, ?, 0, NULL, NULL, ?, 0, NULL, ?)
                    ON CONFLICT(asset_id) DO UPDATE SET modified = excluded.modified,
                        state = excluded.state, at = excluded.at
                """, arguments: [a.id, Int64(a.modified.timeIntervalSince1970 * 1000),
                                 UploadState.skippedPreexisting.rawValue,
                                 Int64(Date().timeIntervalSince1970 * 1000)])
            }
        }
    }

    /// Forgets the "was already there" marks, turning the existing library back into work.
    /// Uploaded and deferred rows are left alone: what already went up must not go again.
    /// Returns how many photos are now waiting.
    @discardableResult
    public func unseed() throws -> Int {
        try dbQueue.write { db in
            let n = try Int.fetchOne(db, sql: "SELECT COUNT(*) FROM uploads WHERE state = ?",
                                     arguments: [UploadState.skippedPreexisting.rawValue]) ?? 0
            try db.execute(sql: "DELETE FROM uploads WHERE state = ?",
                           arguments: [UploadState.skippedPreexisting.rawValue])
            return n
        }
    }

    public func counts() throws -> JournalCounts {
        try dbQueue.read { db in
            func n(_ state: UploadState) throws -> Int {
                try Int.fetchOne(db, sql: "SELECT COUNT(*) FROM uploads WHERE state = ?",
                                 arguments: [state.rawValue]) ?? 0
            }
            return JournalCounts(sent: try n(.sent), skipped: try n(.skippedPreexisting),
                                 deferred: try n(.deferred))
        }
    }

    public func recent(limit: Int) throws -> [JournalEntry] {
        try dbQueue.read { db in
            try Row.fetchAll(db, sql: """
                SELECT asset_id, server_name, state, error, at FROM uploads
                ORDER BY at DESC LIMIT ?
            """, arguments: [limit]).map { row in
                JournalEntry(
                    assetID: row["asset_id"],
                    serverName: row["server_name"] ?? "",
                    state: UploadState(rawValue: row["state"] ?? "") ?? .deferred,
                    error: row["error"],
                    at: Date(timeIntervalSince1970: Double(row["at"] as Int64) / 1000),
                )
            }
        }
    }

    private func put(assetID: String, modified: Date, bytes: Int64, sha: String?,
                     serverName: String?, state: UploadState, error: String?, attempts: Int) throws {
        try dbQueue.write { db in
            try db.execute(sql: """
                INSERT INTO uploads(asset_id, modified, bytes, sha, server_name, state, attempts, error, at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(asset_id) DO UPDATE SET
                    modified = excluded.modified, bytes = excluded.bytes, sha = excluded.sha,
                    server_name = excluded.server_name, state = excluded.state,
                    attempts = excluded.attempts, error = excluded.error, at = excluded.at
            """, arguments: [assetID, Int64(modified.timeIntervalSince1970 * 1000), bytes, sha,
                             serverName, state.rawValue, attempts, error,
                             Int64(Date().timeIntervalSince1970 * 1000)])
        }
    }
}
