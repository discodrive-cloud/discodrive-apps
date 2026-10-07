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
                CREATE TABLE IF NOT EXISTS photo_discovery_cursor(id INTEGER PRIMARY KEY CHECK(id = 1), token BLOB NOT NULL);
                CREATE TABLE IF NOT EXISTS photo_discovery_pending(asset_id TEXT PRIMARY KEY, modified INTEGER NOT NULL);
                CREATE INDEX IF NOT EXISTS idx_photo_pending_modified ON photo_discovery_pending(modified DESC);
            """)
            let version = try Int.fetchOne(db, sql: "PRAGMA user_version") ?? 0
            if version < 1 {
                // Before names were taken from the original, every edited photo went up as
                // FullSizeRender.*, and once those names ran out the rest were recorded as
                // sent without being sent (bytes 0). Put them back into the queue.
                try db.execute(sql: """
                    DELETE FROM uploads WHERE state = ? AND bytes = 0 AND server_name LIKE 'FullSizeRender%'
                    """, arguments: [UploadState.sent.rawValue])
                try db.execute(sql: "PRAGMA user_version = 1")
            }
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

    /// Whether a pass may try this asset: always while it has failed fewer than
    /// `maxAttempts` times, then once per `retryAfter` — a long outage or a missing
    /// destination must not drop a photo from the backup for good.
    public func mayRetry(assetID: String, maxAttempts: Int, retryAfter: TimeInterval,
                         now: Date = Date()) throws -> Bool {
        try dbQueue.read { db in
            guard let row = try Row.fetchOne(db, sql: "SELECT attempts, at FROM uploads WHERE asset_id = ?",
                                             arguments: [assetID]) else { return true }
            let attempts: Int = row["attempts"]
            let at = Date(timeIntervalSince1970: Double(row["at"] as Int64) / 1000)
            return attempts < maxAttempts || now.timeIntervalSince(at) >= retryAfter
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
    ///
    /// Only assets the journal does not know yet are recorded: seeding again (the feature
    /// switched back on) must not turn a sent photo into a "pre-existing" one — "upload
    /// existing photos" would then send it a second time — nor drop a deferred one out of
    /// its retries.
    public func seedPreexisting(_ assets: [(id: String, modified: Date)]) throws {
        try dbQueue.write { db in
            for a in assets {
                try db.execute(sql: """
                    INSERT INTO uploads(asset_id, modified, bytes, sha, server_name, state, attempts, error, at)
                    VALUES (?, ?, 0, NULL, NULL, ?, 0, NULL, ?)
                    ON CONFLICT(asset_id) DO NOTHING
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
            try db.execute(sql: """
                INSERT INTO photo_discovery_pending(asset_id, modified)
                SELECT asset_id, modified FROM uploads WHERE state = ?
                ON CONFLICT(asset_id) DO UPDATE SET modified = excluded.modified
                """, arguments: [UploadState.skippedPreexisting.rawValue])
            try db.execute(sql: "DELETE FROM uploads WHERE state = ?",
                           arguments: [UploadState.skippedPreexisting.rawValue])
            return n
        }
    }

    /// Forgets everything. Called on logout: the next pairing may be another account, and
    /// what was sent to this one says nothing about what that one has.
    public func wipe() throws {
        try dbQueue.write { db in
            try db.execute(sql: "DELETE FROM uploads; DELETE FROM photo_discovery_pending; DELETE FROM photo_discovery_cursor")
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

    public func photoDiscoveryCursor() throws -> Data? {
        try dbQueue.read { try Data.fetchOne($0, sql: "SELECT token FROM photo_discovery_cursor WHERE id = 1") }
    }

    /// The cursor and pending identifiers commit together: a killed process replays
    /// changes or finds their durable work, never advances past unrecorded photos.
    public func applyPhotoDiscovery(_ change: PhotoLibraryDelta, replacing: Bool = false,
                                    seedPreexisting: Bool = false) throws {
        try dbQueue.write { db in
            if replacing { try db.execute(sql: "DELETE FROM photo_discovery_pending; DELETE FROM photo_discovery_cursor") }
            for id in change.deleted {
                try db.execute(sql: "DELETE FROM photo_discovery_pending WHERE asset_id = ?", arguments: [id])
            }
            for asset in change.updated {
                try Task.checkCancellation()
                let modified = Int64(asset.modified.timeIntervalSince1970 * 1000)
                if seedPreexisting {
                    try db.execute(sql: """
                        INSERT OR IGNORE INTO uploads(asset_id, modified, state, at) VALUES (?, ?, ?, ?)
                        """, arguments: [asset.id, modified, UploadState.skippedPreexisting.rawValue,
                                         Int64(Date().timeIntervalSince1970 * 1000)])
                }
                let known = try Bool.fetchOne(db, sql: """
                    SELECT EXISTS(SELECT 1 FROM uploads WHERE asset_id = ? AND modified = ? AND state != ?)
                    """, arguments: [asset.id, modified, UploadState.deferred.rawValue]) ?? false
                if known {
                    try db.execute(sql: "DELETE FROM photo_discovery_pending WHERE asset_id = ?", arguments: [asset.id])
                } else {
                    try db.execute(sql: """
                        INSERT INTO photo_discovery_pending(asset_id, modified) VALUES (?, ?)
                        ON CONFLICT(asset_id) DO UPDATE SET modified = excluded.modified
                        """, arguments: [asset.id, modified])
                }
            }
            try Task.checkCancellation()
            if let token = change.token {
                try db.execute(sql: "INSERT OR REPLACE INTO photo_discovery_cursor(id, token) VALUES (1, ?)", arguments: [token])
            }
        }
    }

    public func pendingPhotos(limit: Int, excluding: Set<String> = [], retryAfter: TimeInterval = 60,
                              now: Date = Date()) throws -> [PhotoAssetStamp] {
        try dbQueue.read { db in
            let placeholders = Array(repeating: "?", count: excluding.count).joined(separator: ",")
            let exclusion = excluding.isEmpty ? "" : "AND p.asset_id NOT IN (\(placeholders))"
            var args: StatementArguments = [UploadState.deferred.rawValue, Int64(now.addingTimeInterval(-retryAfter).timeIntervalSince1970 * 1000)]
            args += StatementArguments(excluding.sorted())
            args += [max(0, limit)]
            return try Row.fetchAll(db, sql: """
                SELECT p.asset_id, p.modified FROM photo_discovery_pending p
                LEFT JOIN uploads u ON u.asset_id = p.asset_id AND u.modified = p.modified
                WHERE (u.state IS NULL OR u.state != ? OR u.at <= ?) \(exclusion)
                ORDER BY p.modified DESC, p.asset_id LIMIT ?
                """, arguments: args).map {
                    PhotoAssetStamp(id: $0["asset_id"], modified: Date(timeIntervalSince1970: Double($0["modified"] as Int64) / 1000))
                }
        }
    }

    public func forgetPendingPhoto(_ asset: PhotoAssetStamp) throws {
        try dbQueue.write { db in
            try db.execute(sql: "DELETE FROM photo_discovery_pending WHERE asset_id = ? AND modified = ?",
                           arguments: [asset.id, Int64((asset.modified.timeIntervalSince1970 * 1000).rounded())])
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
            if state == .sent {
                try db.execute(sql: "DELETE FROM photo_discovery_pending WHERE asset_id = ? AND modified = ?",
                               arguments: [assetID, Int64(modified.timeIntervalSince1970 * 1000)])
            }
        }
    }
}
