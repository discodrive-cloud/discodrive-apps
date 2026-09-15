import Foundation
import GRDB

public enum LocalStatus: Sendable { case none, cached, pinned, stale }

public final class LocalStore {
    let dbQueue: DatabaseQueue
    let contentDir: URL

    public init(dbQueue: DatabaseQueue, contentDir: URL) throws {
        self.dbQueue = dbQueue
        self.contentDir = contentDir
        try FileManager.default.createDirectory(at: contentDir, withIntermediateDirectories: true)
        try dbQueue.write { db in
            try db.execute(sql: """
                CREATE TABLE IF NOT EXISTS local(
                  node_id TEXT PRIMARY KEY, state TEXT NOT NULL,
                  version INTEGER NOT NULL, path TEXT NOT NULL
                );
            """)
        }
    }

    // Convenience initializer — everything in one directory: local.sqlite DB + content/ folder.
    public convenience init(directory: URL) throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let q = try DatabaseQueue(path: directory.appendingPathComponent("local.sqlite").path)
        try self.init(dbQueue: q, contentDir: directory.appendingPathComponent("content"))
    }

    // Separate directories: the DB goes in one place, content in another (on iOS the content
    // lives in Documents so the file tree is visible in Files.app).
    public convenience init(dbDirectory: URL, contentDirectory: URL) throws {
        try FileManager.default.createDirectory(at: dbDirectory, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: contentDirectory, withIntermediateDirectories: true)
        let q = try DatabaseQueue(path: dbDirectory.appendingPathComponent("local.sqlite").path)
        try self.init(dbQueue: q, contentDir: contentDirectory)
    }

    public func status(nodeID: String, serverVersion: Int64) throws -> LocalStatus {
        try dbQueue.read { db in
            guard let row = try Row.fetchOne(db, sql: "SELECT state, version FROM local WHERE node_id = ?", arguments: [nodeID])
            else { return .none }
            let v = row["version"] as Int64
            if v < serverVersion { return .stale }
            return (row["state"] as String) == "pinned" ? .pinned : .cached
        }
    }

    // Local path of the cached copy (mirrors the server tree) — read from the DB.
    public func localURL(nodeID: String) -> URL? {
        guard let p = (try? dbQueue.read { db in
            try String.fetchOne(db, sql: "SELECT path FROM local WHERE node_id=?", arguments: [nodeID])
        }) ?? nil, FileManager.default.fileExists(atPath: p) else { return nil }
        return URL(fileURLWithPath: p)
    }

    // The content directory (used when scanning for import).
    public var contentDirectory: URL { contentDir }

    // Register a file already in the content directory as a local copy (import — no move needed).
    public func registerExisting(nodeID: String, version: Int64, relPath: String) throws {
        let dst = fileURL(forRelPath: relPath)
        guard FileManager.default.fileExists(atPath: dst.path) else { return }
        try dbQueue.write { db in
            try db.execute(sql: """
                INSERT INTO local(node_id,state,version,path) VALUES(?,?,?,?)
                ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, version=excluded.version, path=excluded.path
            """, arguments: [nodeID, "cached", version, dst.path])
        }
    }

    // Safely builds a path under contentDir from a server-relative path
    // (strips ".", "..", and empty segments to prevent directory traversal).
    private func fileURL(forRelPath relPath: String) -> URL {
        let parts = relPath.split(separator: "/").map(String.init)
            .filter { $0 != "." && $0 != ".." && !$0.isEmpty }
        return parts.reduce(contentDir) { $0.appendingPathComponent($1) }
    }

    // relPath — full path from the root (mirrors the server tree, giving sensible names in Finder).
    public func store(nodeID: String, version: Int64, from tmp: URL, pinned: Bool, relPath: String) throws {
        let dst = fileURL(forRelPath: relPath)
        try FileManager.default.createDirectory(at: dst.deletingLastPathComponent(), withIntermediateDirectories: true)
        if FileManager.default.fileExists(atPath: dst.path) { try FileManager.default.removeItem(at: dst) }
        try FileManager.default.moveItem(at: tmp, to: dst)
        try dbQueue.write { db in
            try db.execute(sql: """
                INSERT INTO local(node_id,state,version,path) VALUES(?,?,?,?)
                ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, version=excluded.version, path=excluded.path
            """, arguments: [nodeID, pinned ? "pinned" : "cached", version, dst.path])
        }
    }

    public func pin(nodeID: String) throws {
        try dbQueue.write { db in try db.execute(sql: "UPDATE local SET state='pinned' WHERE node_id=?", arguments: [nodeID]) }
    }
    public func unpin(nodeID: String) throws {
        try dbQueue.write { db in try db.execute(sql: "UPDATE local SET state='cached' WHERE node_id=?", arguments: [nodeID]) }
    }

    public func remove(nodeID: String) throws {
        if let url = localURL(nodeID: nodeID) {
            try? FileManager.default.removeItem(at: url)
            pruneEmptyParents(of: url)
        }
        try dbQueue.write { db in try db.execute(sql: "DELETE FROM local WHERE node_id=?", arguments: [nodeID]) }
    }

    // The folders a download was placed in are only there because of it: once the last
    // file in one is gone the folder goes too, and so on up to the content root. Finder's
    // own housekeeping files do not keep a folder alive.
    private func pruneEmptyParents(of url: URL) {
        let fm = FileManager.default
        let root = contentDir.standardizedFileURL.resolvingSymlinksInPath().path
        var dir = url.deletingLastPathComponent()
        while dir.standardizedFileURL.resolvingSymlinksInPath().path != root, dir.path.count > root.count,
              let entries = try? fm.contentsOfDirectory(atPath: dir.path) {
            guard entries.allSatisfy(LocalOnlyNames.isLocalOnly) else { return }
            guard (try? fm.removeItem(at: dir)) != nil else { return }
            dir = dir.deletingLastPathComponent()
        }
    }

    public func evictCached() throws {
        let ids = try dbQueue.read { db in
            try String.fetchAll(db, sql: "SELECT node_id FROM local WHERE state='cached'")
        }
        for id in ids { try remove(nodeID: id) }
    }
}
