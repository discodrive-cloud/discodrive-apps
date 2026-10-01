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
                CREATE TABLE IF NOT EXISTS vault_conflict_cleanup(
                  node_id TEXT PRIMARY KEY
                );
                CREATE TABLE IF NOT EXISTS vault_dirs(
                  vault_id TEXT NOT NULL, dir_id TEXT NOT NULL, parent_dir_id TEXT NOT NULL,
                  name TEXT NOT NULL, entry_node_id TEXT NOT NULL,
                  PRIMARY KEY(vault_id, dir_id)
                );
            """)
            // The feed seq at which a row was last written; 0 for rows from before. Of two
            // folders at one path (a ghost and the live folder that took the path since),
            // the one written last is the live one.
            if try !db.columns(in: "nodes").contains(where: { $0.name == "seq" }) {
                try db.execute(sql: "ALTER TABLE nodes ADD COLUMN seq INTEGER NOT NULL DEFAULT 0")
            }
            // 0: the directory is there. Otherwise the cursor as of which it was found gone.
            if try !db.columns(in: "vault_dirs").contains(where: { $0.name == "gone_at" }) {
                try db.execute(sql: "ALTER TABLE vault_dirs ADD COLUMN gone_at INTEGER NOT NULL DEFAULT 0")
            }
        }
    }

    // The app and the File Provider extension both apply change pages to this database, each
    // at its own pace. A page one of them fetched a while ago must not undo what the other
    // has applied since — put v1 back over v2, or bring back a node deleted meanwhile. The
    // cursor settles it: it is the sequence number everything up to which is in the index,
    // and it moves in the same transaction as the rows, so a change at or below it has been
    // applied already, by someone, and is skipped whatever it says.
    public func apply(_ changes: [RemoteChange]) throws {
        try dbQueue.write { db in
            let applied = (try String.fetchOne(db, sql: "SELECT value FROM meta WHERE key='cursor'")).flatMap(Int64.init) ?? 0
            let changes = changes.filter { $0.seq > applied }
            guard let last = changes.map(\.seq).max() else { return }
            try db.execute(sql: "INSERT INTO meta(key,value) VALUES('cursor',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
                           arguments: [String(last)])
            // Rows are applied one by one, in feed order, each with its parent resolved at
            // once: a delete further down the page then walks the tree as it stands at that
            // point. Resolving parents only after the whole page left a node moved out of a
            // folder still hanging from it by its old parent id, and a delete of that folder
            // later in the page took the live node along.
            for ch in changes {
                let path = Self.normalize(ch.path)
                if ch.deleted {
                    // By id, where the index has the node — never the feed's path: a purged
                    // node is re-announced with its original path, which a newer node may
                    // hold by now. An id the index does not have leaves nothing to delete.
                    try Self.deleteSubtree(db, nodeID: ch.nodeID)
                    continue
                }
                let name = (path as NSString).lastPathComponent
                // Parents are resolved for the rows this page touches, and for the children of
                // any folder it touches — not for the whole table, which held the write lock
                // for seconds per page on a large tree and starved the other process.
                let parentPath = (path as NSString).deletingLastPathComponent
                let pid: String? = (parentPath.isEmpty || parentPath == "/")
                    ? nil : try String.fetchOne(db, sql: "SELECT id FROM nodes WHERE path = ? AND is_dir = 1 AND id != ? ORDER BY seq DESC LIMIT 1",
                                                arguments: [parentPath, ch.nodeID])
                try db.execute(sql: """
                    INSERT INTO nodes(id,parent_id,name,is_dir,version,content_hash,size,path,seq)
                    VALUES(?,?,?,?,?,?,?,?,?)
                    ON CONFLICT(id) DO UPDATE SET
                      parent_id=excluded.parent_id,
                      name=excluded.name, is_dir=excluded.is_dir, version=excluded.version,
                      content_hash=excluded.content_hash, size=excluded.size, path=excluded.path,
                      seq=excluded.seq
                """, arguments: [ch.nodeID, pid, name, ch.isDir, ch.version, ch.contentHash, ch.size, path, ch.seq])
                if ch.isDir {
                    // Children that arrived before their folder now have a parent to point at.
                    // Case-sensitive (see underSQL): "docs/b" is not a child of "Docs".
                    try db.execute(sql: """
                        UPDATE nodes SET parent_id = ? WHERE \(Self.underSQL) AND instr(substr(path, length(?) + 2), '/') = 0 AND id != ?
                    """, arguments: [ch.nodeID, path, path, path, ch.nodeID])
                }
            }
        }
    }

    // "path is strictly under the folder bound twice as the next two arguments". A prefix
    // compare rather than LIKE: LIKE ignores ASCII case, and the server allows "Docs" and
    // "docs" as siblings, so a LIKE match took the other folder's tree along. It also needs
    // no escaping of "%" and "_".
    static let underSQL = "substr(path, 1, length(?) + 1) = ? || '/'"

    // The node and, for a folder, everything under it — found by identity, never by a path
    // taken from the feed. Paths are not unique: a ghost can still be indexed at a path a live
    // node has taken since, and parent ids are resolved from paths, so under a shared path
    // they cannot be trusted (the newer folder adopts every child at that path). In that case
    // only the node itself is returned; otherwise the subtree is walked through parent ids.
    // An id the index does not have returns nothing.
    static func subtreeIDs(_ db: Database, nodeID: String) throws -> [String] {
        guard let path = try String.fetchOne(db, sql: "SELECT path FROM nodes WHERE id = ?", arguments: [nodeID]) else { return [] }
        let shared = try Int.fetchOne(db, sql: "SELECT COUNT(*) FROM nodes WHERE path = ? AND id != ?", arguments: [path, nodeID]) ?? 0
        if shared > 0 { return [nodeID] }
        return try String.fetchAll(db, sql: """
            WITH RECURSIVE t(id) AS (SELECT ? UNION SELECT c.id FROM nodes c JOIN t ON c.parent_id = t.id)
            SELECT id FROM t
        """, arguments: [nodeID])
    }

    // Applies a delete of one node (see subtreeIDs). Returns the ids removed.
    @discardableResult
    static func deleteSubtree(_ db: Database, nodeID: String) throws -> [String] {
        let ids = try subtreeIDs(db, nodeID: nodeID)
        for id in ids { try db.execute(sql: "DELETE FROM nodes WHERE id = ?", arguments: [id]) }
        return ids
    }

    // Applies a delete the change feed never delivered — the server answered that the node
    // does not exist (APIError.nodeNotFound) — the same way `apply` applies a delete event.
    // The cursor does not move. Returns the ids that left the index; empty for an unknown id.
    @discardableResult
    public func forget(nodeID: String) throws -> [String] {
        try dbQueue.write { db in try Self.deleteSubtree(db, nodeID: nodeID) }
    }

    // Runs a request about nodeID. When the server answers that the node does not exist, the
    // node and its subtree are forgotten and APIError.nodeNotFound is rethrown, for the
    // caller to say so. Any other outcome passes through untouched.
    // For a request whose 404 can also be about something else it names — a share's
    // recipient, a version to restore — pass `confirm`: the node is forgotten only when it
    // answers that the node does not exist; otherwise (or when asking fails) the answer
    // stays a plain `.http(404)` and nothing is forgotten.
    public func forgettingIfGone<T>(_ nodeID: String, confirm: ((String) async throws -> Bool)? = nil,
                                    _ body: () async throws -> T) async throws -> T {
        do { return try await body() }
        catch APIError.nodeNotFound {
            if let confirm, (try? await confirm(nodeID)) != false { throw APIError.http(404) }
            try forget(nodeID: nodeID)
            throw APIError.nodeNotFound
        }
    }

    // Deletes nodeID on the server through body. A node the server no longer has is already
    // what was asked for: it is forgotten and the delete succeeds. Returns the ids forgotten
    // that way (empty after an ordinary delete).
    @discardableResult
    public func deleteForgettingGone(nodeID: String, _ body: () async throws -> Void) async throws -> [String] {
        do {
            try await body()
            return []
        } catch APIError.nodeNotFound {
            return try forget(nodeID: nodeID)
        }
    }

    // After a move answered APIError.nodeNotFound. The server says the same whether the
    // moved node or the destination folder is missing, so each is asked about through
    // `exists` and the ones gone are forgotten. A question that fails forgets nothing.
    // Returns the ids that left the index.
    @discardableResult
    public func forgetGoneAfterMove(nodeID: String, parentID: String?,
                                    exists: (String) async throws -> Bool) async -> [String] {
        var forgotten: [String] = []
        for id in [nodeID, parentID].compactMap({ $0 }) {
            guard let alive = try? await exists(id), !alive else { continue }
            forgotten += (try? forget(nodeID: id)) ?? []
        }
        return forgotten
    }

    // The one spelling of a server path the index, the local copies and the import agree
    // on: segments joined by "/", no leading or trailing slash, no empty segments.
    public static func normalize(_ path: String) -> String {
        path.split(separator: "/", omittingEmptySubsequences: true).joined(separator: "/")
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

    // Every node, folders first then by path. File Provider uses enumerationPage instead
    // to avoid loading the entire index into its memory-limited extension process.
    public func allNodes() throws -> [Node] {
        try dbQueue.read { db in
            try Row.fetchAll(db, sql: "SELECT * FROM nodes ORDER BY is_dir DESC, path").map(Self.rowToNode)
        }
    }

    // Keyset pagination keeps memory bounded and does not skip rows when a preceding
    // node is deleted between File Provider requests. The system sorts the displayed items.
    public func enumerationPage(parentID: String?, workingSet: Bool, afterID: String?, limit: Int) throws -> [Node] {
        precondition(limit > 0)
        return try dbQueue.read { db in
            var conditions: [String] = []
            var arguments = StatementArguments()
            if !workingSet {
                if let parentID {
                    conditions.append("parent_id = ?")
                    arguments += [parentID]
                } else {
                    conditions.append("parent_id IS NULL")
                }
            }
            if let afterID {
                conditions.append("id > ?")
                arguments += [afterID]
            }
            let filter = conditions.isEmpty ? "" : " WHERE " + conditions.joined(separator: " AND ")
            arguments += [limit]
            return try Row.fetchAll(db, sql: "SELECT * FROM nodes" + filter + " ORDER BY id LIMIT ?",
                                    arguments: arguments).map(Self.rowToNode)
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
                  parent_dir_id=excluded.parent_dir_id, name=excluded.name, entry_node_id=excluded.entry_node_id, gone_at=0
            """, arguments: [vault, dirID, parentDirID, name, entryNodeID])
        }
    }

    public func vaultDir(vault: String, dirID: String) throws -> VaultDir? {
        try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT * FROM vault_dirs WHERE vault_id = ? AND dir_id = ? AND gone_at = 0", arguments: [vault, dirID])
                .map { VaultDir(dirID: $0["dir_id"], parentDirID: $0["parent_dir_id"], name: $0["name"], entryNodeID: $0["entry_node_id"]) }
        }
    }

    public func vaultDirs(vault: String) throws -> [VaultDir] {
        try dbQueue.read { db in
            try Row.fetchAll(db, sql: "SELECT * FROM vault_dirs WHERE vault_id = ? AND gone_at = 0", arguments: [vault])
                .map { VaultDir(dirID: $0["dir_id"], parentDirID: $0["parent_dir_id"], name: $0["name"], entryNodeID: $0["entry_node_id"]) }
        }
    }

    // A directory found gone stays in the map, marked: its id is what Finder knows it by,
    // and nothing else leads from the deleted node back to it. The mark carries the cursor
    // of the delta that reported it, so the same delta asked for again reports it again.
    public func markVaultDirGone(vault: String, dirID: String, at cursor: Int64) throws {
        try dbQueue.write { db in
            try db.execute(sql: "UPDATE vault_dirs SET gone_at = ? WHERE vault_id = ? AND dir_id = ? AND gone_at = 0",
                           arguments: [max(cursor, 1), vault, dirID])
        }
    }

    // Anchors belong to independent enumerators. Reading a newer anchor must not
    // discard deletions an older enumerator still needs. Retain them until map reset.
    public func vaultDirsGone(vault: String, since anchor: Int64 = 0) throws -> [String] {
        try dbQueue.read { db in
            try String.fetchAll(db, sql: "SELECT dir_id FROM vault_dirs WHERE vault_id = ? AND gone_at > 0 AND gone_at >= ?",
                                arguments: [vault, anchor])
        }
    }

    // Node ids survive vault renames. The index is scoped to one account, so any open
    // vault can finish that account's pending cleanup after a restart or a rename.
    public func queueVaultConflictRemoval(nodeID: String) throws {
        try dbQueue.write { db in
            try db.execute(sql: "INSERT OR IGNORE INTO vault_conflict_cleanup(node_id) VALUES(?)", arguments: [nodeID])
        }
    }

    public func pendingVaultConflictRemovals() throws -> [String] {
        try dbQueue.read { db in
            try String.fetchAll(db, sql: "SELECT node_id FROM vault_conflict_cleanup ORDER BY node_id")
        }
    }

    public func finishVaultConflictRemoval(nodeID: String) throws {
        try dbQueue.write { db in
            try db.execute(sql: "DELETE FROM vault_conflict_cleanup WHERE node_id = ?", arguments: [nodeID])
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
