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
    // Writes begin IMMEDIATE: the lock is taken up front, where the busy timeout applies.
    // A DEFERRED transaction reads first and then finds its snapshot stale once the other
    // process commits, which SQLite reports as "database is locked" without waiting.
    public convenience init(path: String) throws {
        var config = Configuration()
        config.busyMode = .timeout(30)
        config.defaultTransactionKind = .immediate
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
                CREATE TABLE IF NOT EXISTS vault_dirs(
                  vault_id TEXT NOT NULL, dir_id TEXT NOT NULL, parent_dir_id TEXT NOT NULL,
                  name TEXT NOT NULL, entry_node_id TEXT NOT NULL,
                  PRIMARY KEY(vault_id, dir_id)
                );
            """)
        }
    }

    public func apply(_ changes: [RemoteChange]) throws {
        try dbQueue.write { db in
            for ch in changes {
                let path = Self.normalize(ch.path)
                if ch.deleted {
                    guard !path.isEmpty else {
                        try db.execute(sql: "DELETE FROM nodes WHERE id = ?", arguments: [ch.nodeID])
                        continue
                    }
                    // The folder's own subtree only: "a_" must not take "ab/…" with it.
                    try db.execute(sql: "DELETE FROM nodes WHERE id = ? OR path LIKE ? ESCAPE '\\'",
                                   arguments: [ch.nodeID, Self.likePrefix(path) + "/%"])
                    continue
                }
                let name = (path as NSString).lastPathComponent
                try db.execute(sql: """
                    INSERT INTO nodes(id,parent_id,name,is_dir,version,content_hash,size,path)
                    VALUES(?,?,?,?,?,?,?,?)
                    ON CONFLICT(id) DO UPDATE SET
                      name=excluded.name, is_dir=excluded.is_dir, version=excluded.version,
                      content_hash=excluded.content_hash, size=excluded.size, path=excluded.path
                """, arguments: [ch.nodeID, nil, name, ch.isDir, ch.version, ch.contentHash, ch.size, path])
            }
            // Parents are resolved for the rows this batch touched, and for the children of
            // any folder it touched — not for the whole table, which held the write lock
            // for seconds per page on a large tree and starved the other process.
            for ch in changes where !ch.deleted {
                let path = Self.normalize(ch.path)
                let parentPath = (path as NSString).deletingLastPathComponent
                let pid: String? = (parentPath.isEmpty || parentPath == "/")
                    ? nil : try String.fetchOne(db, sql: "SELECT id FROM nodes WHERE path = ?", arguments: [parentPath])
                try db.execute(sql: "UPDATE nodes SET parent_id = ? WHERE id = ?", arguments: [pid, ch.nodeID])
                if ch.isDir {
                    // Children that arrived before their folder now have a parent to point at.
                    try db.execute(sql: """
                        UPDATE nodes SET parent_id = ? WHERE path LIKE ? ESCAPE '\\' AND path NOT LIKE ? ESCAPE '\\' AND id != ?
                    """, arguments: [ch.nodeID, Self.likePrefix(path) + "/%", Self.likePrefix(path) + "/%/%", ch.nodeID])
                }
            }
        }
    }

    // The one spelling of a server path the index, the local copies and the import agree
    // on: segments joined by "/", no leading or trailing slash, no empty segments.
    public static func normalize(_ path: String) -> String {
        path.split(separator: "/", omittingEmptySubsequences: true).joined(separator: "/")
    }

    // A path as a LIKE prefix, with the pattern characters it may contain escaped.
    static func likePrefix(_ path: String) -> String {
        path.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "%", with: "\\%").replacingOccurrences(of: "_", with: "\\_")
    }

    public func node(id: String) throws -> Node? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM nodes WHERE id = ?", arguments: [id])
                .map(Self.rowToNode)
        }
    }

    public func node(atPath path: String) throws -> Node? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM nodes WHERE path = ?", arguments: [Self.normalize(path)])
                .map(Self.rowToNode)
        }
    }

    // The cursor only moves forward: the app and the File Provider extension both apply
    // change pages to this database, and whichever finishes an older page later must not
    // wind it back behind what the other already recorded.
    // Runs `body` inside a write transaction: how tests stand in for the other process.
    func holdingWrite(_ body: () -> Void) throws {
        try dbQueue.write { db in
            try db.execute(sql: "INSERT INTO meta(key,value) VALUES('hold','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
            body()
        }
    }

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

    // Whether `path` is a Cryptomator vault or lies inside one: the folder itself, or any
    // ancestor, holds a `vault.cryptomator`. Finder must not write there — the ciphertext
    // layout is the vault's, not the user's.
    public func isInsideVault(path: String) throws -> Bool {
        var parts = path.split(separator: "/").map(String.init)
        while !parts.isEmpty {
            // Paths are stored as the server sends them, without a leading slash.
            if try node(atPath: parts.joined(separator: "/") + "/vault.cryptomator") != nil { return true }
            parts.removeLast()
        }
        return false
    }

    // The server path of `name` inside `folder` ("" = root), in the index's own spelling.
    public static func path(in folder: String, name: String) -> String {
        folder.isEmpty ? name : folder + "/" + name
    }

    // MARK: - Vault directory map

    // Cryptomator names a directory's storage by a hash of its id and keeps no way back
    // from a directory to its parent, so the extension records what it learns while
    // enumerating an unlocked vault: which directory id hangs where, under what name.
    public struct VaultDir: Equatable, Sendable {
        public let dirID: String
        public let parentDirID: String
        public let name: String
        public let entryNodeID: String   // the ciphertext entry (name.c9r) this directory is listed by
    }

    public func rememberVaultDir(vault: String, dirID: String, parentDirID: String, name: String, entryNodeID: String) throws {
        try dbQueue.write { db in
            try db.execute(sql: """
                INSERT INTO vault_dirs(vault_id, dir_id, parent_dir_id, name, entry_node_id) VALUES(?,?,?,?,?)
                ON CONFLICT(vault_id, dir_id) DO UPDATE SET
                  parent_dir_id=excluded.parent_dir_id, name=excluded.name, entry_node_id=excluded.entry_node_id
            """, arguments: [vault, dirID, parentDirID, name, entryNodeID])
        }
    }

    public func vaultDir(vault: String, dirID: String) throws -> VaultDir? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM vault_dirs WHERE vault_id = ? AND dir_id = ?", arguments: [vault, dirID])
                .map { VaultDir(dirID: $0["dir_id"], parentDirID: $0["parent_dir_id"], name: $0["name"], entryNodeID: $0["entry_node_id"]) }
        }
    }

    public func vaultDirs(vault: String) throws -> [VaultDir] {
        try dbQueue.read { db in
            try Row.fetchAll(db, sql: "SELECT * FROM vault_dirs WHERE vault_id = ?", arguments: [vault])
                .map { VaultDir(dirID: $0["dir_id"], parentDirID: $0["parent_dir_id"], name: $0["name"], entryNodeID: $0["entry_node_id"]) }
        }
    }

    public func forgetVaultDirs(vault: String) throws {
        try dbQueue.write { db in try db.execute(sql: "DELETE FROM vault_dirs WHERE vault_id = ?", arguments: [vault]) }
    }

    static func rowToNode(_ row: Row) -> Node {
        Node(id: row["id"], parentID: row["parent_id"], name: row["name"],
             isDir: (row["is_dir"] as Int64) != 0, version: row["version"],
             contentHash: row["content_hash"], size: row["size"], path: row["path"])
    }
}
