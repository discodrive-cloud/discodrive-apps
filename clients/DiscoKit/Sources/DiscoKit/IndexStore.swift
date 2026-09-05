import Foundation
import GRDB

public final class IndexStore: @unchecked Sendable {   // dbQueue (GRDB) is internally synchronized
    let dbQueue: DatabaseQueue

    public init(dbQueue: DatabaseQueue) throws {
        self.dbQueue = dbQueue
        try migrate()
    }

    // On disk the index is shared between the app and the File Provider extension, two
    // processes that both apply change pages. WAL lets one read while the other writes,
    // and the busy timeout makes a second writer wait instead of failing with "locked".
    public convenience init(path: String) throws {
        var config = Configuration()
        config.busyMode = .timeout(10)
        config.prepareDatabase { db in try db.execute(sql: "PRAGMA journal_mode = WAL") }
        try self.init(dbQueue: try DatabaseQueue(path: path, configuration: config))
    }

    private func migrate() throws {
        try dbQueue.write { db in
            try db.execute(sql: """
                CREATE TABLE IF NOT EXISTS nodes(
                  id TEXT PRIMARY KEY, parent_id TEXT, name TEXT NOT NULL,
                  is_dir INTEGER NOT NULL, version INTEGER NOT NULL,
                  content_hash TEXT NOT NULL, size INTEGER NOT NULL,
                  path TEXT NOT NULL DEFAULT ''
                );
                CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id);
                CREATE INDEX IF NOT EXISTS idx_nodes_path ON nodes(path);
                CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
            """)
        }
    }

    public func apply(_ changes: [RemoteChange]) throws {
        try dbQueue.write { db in
            for ch in changes {
                if ch.deleted {
                    try db.execute(sql: "DELETE FROM nodes WHERE id = ? OR path LIKE ?",
                                   arguments: [ch.nodeID, ch.path + "/%"])
                    continue
                }
                let name = (ch.path as NSString).lastPathComponent
                try db.execute(sql: """
                    INSERT INTO nodes(id,parent_id,name,is_dir,version,content_hash,size,path)
                    VALUES(?,?,?,?,?,?,?,?)
                    ON CONFLICT(id) DO UPDATE SET
                      name=excluded.name, is_dir=excluded.is_dir, version=excluded.version,
                      content_hash=excluded.content_hash, size=excluded.size, path=excluded.path
                """, arguments: [ch.nodeID, nil, name, ch.isDir, ch.version, ch.contentHash, ch.size, ch.path])
            }
            let rows = try Row.fetchAll(db, sql: "SELECT id, path FROM nodes")
            var pathToID: [String: String] = [:]
            for r in rows { pathToID[r["path"]] = r["id"] }
            for r in rows {
                let path = r["path"] as String
                let parentPath = (path as NSString).deletingLastPathComponent
                let pid: String? = (parentPath.isEmpty || parentPath == "/") ? nil : pathToID[parentPath]
                try db.execute(sql: "UPDATE nodes SET parent_id = ? WHERE id = ?",
                               arguments: [pid, r["id"] as String])
            }
        }
    }

    public func node(id: String) throws -> Node? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM nodes WHERE id = ?", arguments: [id])
                .map(Self.rowToNode)
        }
    }

    public func node(atPath path: String) throws -> Node? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM nodes WHERE path = ?", arguments: [path])
                .map(Self.rowToNode)
        }
    }

    // The cursor only moves forward: the app and the File Provider extension both apply
    // change pages to this database, and whichever finishes an older page later must not
    // wind it back behind what the other already recorded.
    public func setCursor(_ value: Int64) throws {
        try dbQueue.write { db in
            let current = (try String.fetchOne(db, sql: "SELECT value FROM meta WHERE key='cursor'")).flatMap(Int64.init) ?? 0
            guard value > current else { return }
            try db.execute(sql: "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                           arguments: [String(value)])
        }
    }

    // Every node, folders first then by path — what the working-set enumerator hands the
    // system on its first pass.
    public func allNodes() throws -> [Node] {
        try dbQueue.read { db in
            try Row.fetchAll(db, sql: "SELECT * FROM nodes ORDER BY is_dir DESC, path").map(Self.rowToNode)
        }
    }

    public func cursor() throws -> Int64 {
        try dbQueue.read { db in
            (try String.fetchOne(db, sql: "SELECT value FROM meta WHERE key='cursor'")).flatMap(Int64.init) ?? 0
        }
    }

    public func children(of parentID: String?) throws -> [Node] {
        try dbQueue.read { db in
            let rows: [Row]
            if let parentID {
                rows = try Row.fetchAll(db, sql: "SELECT * FROM nodes WHERE parent_id = ? ORDER BY is_dir DESC, name", arguments: [parentID])
            } else {
                rows = try Row.fetchAll(db, sql: "SELECT * FROM nodes WHERE parent_id IS NULL ORDER BY is_dir DESC, name")
            }
            return rows.map(Self.rowToNode)
        }
    }

    static func rowToNode(_ row: Row) -> Node {
        Node(id: row["id"], parentID: row["parent_id"], name: row["name"],
             isDir: (row["is_dir"] as Int64) != 0, version: row["version"],
             contentHash: row["content_hash"], size: row["size"], path: row["path"])
    }
}
