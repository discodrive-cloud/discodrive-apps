import Foundation
import GRDB

/// Durable transfer metadata. The exported asset and the current chunk live beside this
/// record until the server confirms publication. Credentials never enter the queue DB.
public struct PhotoTransfer: Codable, Sendable, Equatable {
    public var id: String
    public var account: String
    public var assetID: String
    public var modified: Date
    public var created: Date
    public var parentID: String
    public var name: String
    public var sha: String
    public var bytes: Int64
    public var uploadID: String?
    // Optional for records written by the initial 8 MiB queue. Keep that checkpoint's
    // offsets unchanged; new jobs reduce expensive iOS wake-ups with 64 MiB parts.
    public var chunkSize: Int?
    public var transferChunkSize: Int { chunkSize ?? ChunkedUploader.defaultChunkSize }
    public var nextChunk = 0
    public var attempts = 0
    public var retryAt = Date.distantPast
    public var lastError: String?
    // Optional: records written before these existed must still decode.
    public var problem: UploadProblem?
    public var failedAt: Date?
    /// Which file of the asset this is. Nil: a job queued by a former version, one file
    /// per asset (see ``LegacyUpload``).
    public var part: PhotoPart?
    /// The edit replaces its own earlier upload: it goes up under `name` as a new version
    /// of that file instead of looking for a free name.
    public var replaces: Bool?
    public var replacesFile: Bool { replaces ?? false }

    public init(account: String, assetID: String, modified: Date, created: Date,
                parentID: String, name: String, sha: String, bytes: Int64,
                part: PhotoPart? = nil, replaces: Bool = false) {
        id = UUID().uuidString
        self.account = account; self.assetID = assetID; self.modified = modified
        self.created = created; self.parentID = parentID; self.name = name
        self.sha = sha; self.bytes = bytes
        self.chunkSize = 64 << 20
        self.part = part
        self.replaces = replaces ? true : nil
    }

    public mutating func deferRetry(_ error: Error, now: Date = Date()) {
        deferRetry(String(describing: error), now: now)
        problem = UploadProblem.classify(error)
    }

    public mutating func deferRetry(_ error: String, now: Date = Date()) {
        attempts += 1
        lastError = String(error.prefix(300))
        problem = .other
        failedAt = now
        // Keep retrying; a temporary outage never silently parks a photo for a day.
        retryAt = now.addingTimeInterval(min(3600, 30 * pow(2, Double(min(attempts - 1, 7)))))
    }

    /// After a step went through: the earlier failure no longer describes this job.
    public mutating func clearFailure() {
        attempts = 0; retryAt = .distantPast
        lastError = nil; problem = nil; failedAt = nil
    }
}

public final class PhotoTransferStore: @unchecked Sendable {
    private let db: DatabaseQueue
    public let directory: URL

    public init(directory: URL) throws {
        self.directory = directory
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        var excluded = directory
        var values = URLResourceValues(); values.isExcludedFromBackup = true
        try excluded.setResourceValues(values)
        #if os(iOS)
        try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: directory.path)
        #endif
        db = try DatabaseQueue(path: directory.appendingPathComponent("queue.sqlite").path)
        try db.write { db in
            // A former queue holds one job per asset version; parts need the part in the key.
            let former = try db.tableExists("photo_transfers")
                && !(try db.columns(in: "photo_transfers").contains { $0.name == "part" })
            if former { try db.execute(sql: "ALTER TABLE photo_transfers RENAME TO photo_transfers_v1") }
            try db.execute(sql: """
                CREATE TABLE IF NOT EXISTS photo_transfers (
                    id TEXT PRIMARY KEY, account TEXT NOT NULL, asset TEXT NOT NULL,
                    modified INTEGER NOT NULL, part TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL,
                    UNIQUE(account, asset, modified, part)
                )
                """)
            if former {
                try db.execute(sql: """
                    INSERT INTO photo_transfers(id, account, asset, modified, part, payload)
                    SELECT id, account, asset, modified, '', payload FROM photo_transfers_v1;
                    DROP TABLE photo_transfers_v1;
                    """)
            }
        }
    }

    public func all(account: String) throws -> [PhotoTransfer] {
        try db.read { db in
            try Data.fetchAll(db, sql: "SELECT payload FROM photo_transfers WHERE account = ? ORDER BY rowid", arguments: [account])
                .map { try JSONDecoder().decode(PhotoTransfer.self, from: $0) }
        }
    }

    public func contains(account: String, assetID: String, modified: Date) throws -> Bool {
        try db.read { db in
            try Bool.fetchOne(db, sql: "SELECT EXISTS(SELECT 1 FROM photo_transfers WHERE account = ? AND asset = ? AND modified = ?)",
                              arguments: [account, assetID, Int64(modified.timeIntervalSince1970 * 1000)]) ?? false
        }
    }

    public func save(_ job: PhotoTransfer) throws {
        let payload = try JSONEncoder().encode(job)
        try db.write { db in try Self.insert(job, payload: payload, into: db) }
    }

    private static func insert(_ job: PhotoTransfer, payload: Data, into db: Database) throws {
        try db.execute(sql: """
            INSERT INTO photo_transfers(id, account, asset, modified, part, payload) VALUES (?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET payload = excluded.payload
            """, arguments: [job.id, job.account, job.assetID, Int64(job.modified.timeIntervalSince1970 * 1000),
                             job.part?.rawValue ?? "", payload])
    }

    /// Moves only an app-owned export, never a PhotoKit original. Persist before handing
    /// any body to URLSession. A failed insert leaves no untracked exported file behind.
    public func stage(_ job: PhotoTransfer, exportedFile: URL) throws {
        let folder = directory.appendingPathComponent(job.id, isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        do {
            try FileManager.default.moveItem(at: exportedFile, to: assetURL(job))
            #if os(iOS)
            try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: assetURL(job).path)
            #endif
            try save(job)
        } catch {
            try? FileManager.default.removeItem(at: folder)
            throw error
        }
    }

    /// Stages the parts of one asset together: either all are queued or none is. A part
    /// published while its sibling is still being queued would mark the asset done.
    public func stage(_ items: [(job: PhotoTransfer, file: URL)]) throws {
        var moved: [PhotoTransfer] = []
        do {
            for (job, file) in items {
                let folder = directory.appendingPathComponent(job.id, isDirectory: true)
                try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
                moved.append(job)
                try FileManager.default.moveItem(at: file, to: assetURL(job))
                #if os(iOS)
                try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: assetURL(job).path)
                #endif
            }
            let payloads = try items.map { try JSONEncoder().encode($0.job) }
            try db.write { db in
                for (item, payload) in zip(items, payloads) { try Self.insert(item.job, payload: payload, into: db) }
            }
        } catch {
            for job in moved { try? FileManager.default.removeItem(at: directory.appendingPathComponent(job.id, isDirectory: true)) }
            for (_, file) in items { try? FileManager.default.removeItem(at: file) }
            throw error
        }
    }

    public func assetURL(_ job: PhotoTransfer) -> URL {
        directory.appendingPathComponent(job.id, isDirectory: true).appendingPathComponent("asset")
    }

    public func chunkURL(_ job: PhotoTransfer) -> URL {
        directory.appendingPathComponent(job.id, isDirectory: true).appendingPathComponent("chunk")
    }

    public func writeChunk(_ job: PhotoTransfer, chunkSize: Int? = nil) throws -> URL {
        let chunkSize = chunkSize ?? job.transferChunkSize
        guard chunkSize > 0, job.nextChunk >= 0, job.bytes >= 0,
              Int64(job.nextChunk) <= job.bytes / Int64(chunkSize) else { throw APIError.badResponse }
        let offset = Int64(job.nextChunk) * Int64(chunkSize)
        guard offset < job.bytes else { throw APIError.badResponse }
        let source = assetURL(job)
        let size = try FileManager.default.attributesOfItem(atPath: source.path)[.size] as? NSNumber
        guard size?.int64Value == job.bytes else { throw UploadError.fileChangedDuringUpload }
        if offset == 0, job.bytes <= Int64(chunkSize) { return source }
        let handle = try FileHandle(forReadingFrom: source)
        defer { try? handle.close() }
        try handle.seek(toOffset: UInt64(offset))
        let url = chunkURL(job)
        let tmp = url.appendingPathExtension("building")
        // Only an idle job can prepare a chunk; no live URLSession task owns this path.
        try? FileManager.default.removeItem(at: tmp)
        guard FileManager.default.createFile(atPath: tmp.path, contents: nil) else { throw CocoaError(.fileWriteUnknown) }
        let output = try FileHandle(forWritingTo: tmp)
        defer { try? output.close(); try? FileManager.default.removeItem(at: tmp) }
        var remaining = min(Int64(chunkSize), job.bytes - offset)
        while remaining > 0 {
            let data = try handle.read(upToCount: Int(min(remaining, 1 << 20))) ?? Data()
            guard !data.isEmpty else { throw UploadError.fileChangedDuringUpload }
            try output.write(contentsOf: data)
            remaining -= Int64(data.count)
        }
        try output.close()
        try? FileManager.default.removeItem(at: url)
        try FileManager.default.moveItem(at: tmp, to: url)
        #if os(iOS)
        try FileManager.default.setAttributes([.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication], ofItemAtPath: url.path)
        #endif
        return url
    }

    public func remove(_ job: PhotoTransfer) throws {
        try db.write { db in try db.execute(sql: "DELETE FROM photo_transfers WHERE id = ? AND account = ?", arguments: [job.id, job.account]) }
        try? FileManager.default.removeItem(at: directory.appendingPathComponent(job.id, isDirectory: true))
    }

    /// Removes what no current job owns: queues of former pairings (their background
    /// session is gone with them) and job folders whose record never got written. Returns
    /// the bytes freed. Call it before this account's queue starts moving.
    @discardableResult
    public func sweep(keepingAccount account: String) throws -> Int64 {
        let stale = try db.read { db in
            try String.fetchAll(db, sql: "SELECT DISTINCT account FROM photo_transfers WHERE account != ?", arguments: [account])
        }
        var freed: Int64 = 0
        for other in stale {
            for job in try all(account: other) {
                freed += Self.size(of: directory.appendingPathComponent(job.id, isDirectory: true))
                try remove(job)
            }
        }
        let known = Set(try db.read { db in try String.fetchAll(db, sql: "SELECT id FROM photo_transfers") })
        let entries = try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.isDirectoryKey])
        for entry in entries where (try? entry.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true
            && !known.contains(entry.lastPathComponent) {
            freed += Self.size(of: entry)
            try FileManager.default.removeItem(at: entry)
        }
        return freed
    }

    private static func size(of folder: URL) -> Int64 {
        let files = FileManager.default.enumerator(at: folder, includingPropertiesForKeys: [.fileSizeKey])
        var total: Int64 = 0
        while let file = files?.nextObject() as? URL {
            total += Int64((try? file.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
        }
        return total
    }

    public func wipe(account: String) throws {
        for job in try all(account: account) { try remove(job) }
    }
}
