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
            // Where the copy sits under the content directory, in the index's spelling. It
            // is what says "this local file is a registered copy", and unlike the absolute
            // path it survives the container moving.
            if try !db.columns(in: "local").contains(where: { $0.name == "rel_path" }) {
                try db.execute(sql: "ALTER TABLE local ADD COLUMN rel_path TEXT NOT NULL DEFAULT ''")
            }
            // A conflict copy the import registered under the name it was dropped in as,
            // until the index says what the server called it.
            if try !db.columns(in: "local").contains(where: { $0.name == "awaits_name" }) {
                try db.execute(sql: "ALTER TABLE local ADD COLUMN awaits_name INTEGER NOT NULL DEFAULT 0")
            }
            let unplaced = try Row.fetchAll(db, sql: "SELECT node_id, path FROM local WHERE rel_path = ''")
                .map { (id: $0["node_id"] as String, path: $0["path"] as String) }
            for (id, rel) in Self.recoveredRelPaths(ofRecorded: unplaced, under: contentDir) {
                try db.execute(sql: "UPDATE local SET rel_path = ? WHERE node_id = ?", arguments: [rel, id])
            }
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

    // A copy is only as good as the file behind it: a row whose file someone removed in
    // Finder reads as "not downloaded", so the next open fetches it again. The row stays,
    // and with it the pin the user asked for.
    public func status(nodeID: String, serverVersion: Int64) throws -> LocalStatus {
        let row = try dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT state, version FROM local WHERE node_id = ?", arguments: [nodeID])
        }
        guard let row, localURL(nodeID: nodeID) != nil else { return .none }
        if (row["version"] as Int64) < serverVersion { return .stale }
        return (row["state"] as String) == "pinned" ? .pinned : .cached
    }

    // Local path of the cached copy (mirrors the server tree) — read from the DB.
    public func localURL(nodeID: String) -> URL? {
        guard let url = registeredURL(nodeID: nodeID), FileManager.default.fileExists(atPath: url.path) else { return nil }
        return url
    }

    // Where the row says the copy is, whether or not the file is still there.
    private func registeredURL(nodeID: String) -> URL? {
        guard let row = (try? dbQueue.read { db in
            try Row.fetchOne(db, sql: "SELECT path, rel_path FROM local WHERE node_id=?", arguments: [nodeID])
        }) ?? nil else { return nil }
        let rel = row["rel_path"] as String
        return rel.isEmpty ? URL(fileURLWithPath: row["path"] as String) : fileURL(forRelPath: rel)
    }

    // The content directory (used when scanning for import).
    public var contentDirectory: URL { contentDir }

    // MARK: - Import: what the user put here themselves

    /// A file found in the content directory that no row claims.
    public struct UnregisteredFile: Equatable, Sendable {
        public let url: URL
        public let relPath: String   // in the index's spelling: no leading slash
    }

    /// The files in the content directory that are not registered local copies — the only
    /// ones an import may treat as the user's own. Registration is by local path, not by
    /// what the server index holds: a copy of a file since deleted or renamed on the
    /// server is still a copy, never something new to send back up.
    ///
    /// Rows written before relative paths whose place could not be recovered (the container
    /// had moved before this build first ran) still claim something, only it is no longer
    /// known what. A file such a row may have meant — its recorded path ends in the file's
    /// own — is not offered: not knowing whose it is does not make it the user's.
    public func unregisteredFiles() throws -> [UnregisteredFile] {
        let claims = try Claims(self)
        let fm = FileManager.default
        guard let en = fm.enumerator(at: contentDir, includingPropertiesForKeys: [.isRegularFileKey],
                                     options: [.skipsHiddenFiles]) else { return [] }
        var out: [UnregisteredFile] = []
        for url in en.allObjects.compactMap({ $0 as? URL }) {
            guard (try? url.resourceValues(forKeys: [.isRegularFileKey]))?.isRegularFile == true,
                  !LocalOnlyNames.isLocalOnly(url.lastPathComponent),
                  let rel = Self.relPath(of: url, under: contentDir),
                  !claims.cover(rel) else { continue }
            out.append(UnregisteredFile(url: url, relPath: rel))
        }
        return out.sorted { $0.relPath < $1.relPath }
    }

    /// Whether a file found by `unregisteredFiles()` is still nobody's. Asked again right
    /// before it is uploaded: the scan is a while ago by then, and a download may have
    /// put a registered copy in its place since.
    public func isStillUnregistered(_ file: UnregisteredFile) throws -> Bool {
        guard FileManager.default.fileExists(atPath: file.url.path) else { return false }
        return try !Claims(self).cover(file.relPath)
    }

    /// What a file was when its upload began: which file (not merely which path), how long
    /// and last written when.
    public struct FileStamp: Equatable {
        let identity: NSObject
        let size: Int
        let modified: Date
        public static func == (a: FileStamp, b: FileStamp) -> Bool {
            a.identity.isEqual(b.identity) && a.size == b.size && a.modified == b.modified
        }
    }

    public func stamp(of url: URL) -> FileStamp? {
        var url = url
        url.removeAllCachedResourceValues()
        guard let v = try? url.resourceValues(forKeys: [.fileResourceIdentifierKey, .fileSizeKey, .contentModificationDateKey]),
              let id = v.fileResourceIdentifier as? NSObject, let size = v.fileSize, let modified = v.contentModificationDate else { return nil }
        return FileStamp(identity: id, size: size, modified: modified)
    }

    /// Registers an imported file as the copy of the node its upload became — if it still
    /// is the file that was uploaded. An upload reads from the file it opened; a download
    /// of the same path meanwhile puts another file there and registers it, and the bytes
    /// lying at the path are then the download's, not the ones the server was sent. That
    /// registration stands, and the uploaded node is left to be downloaded like any other.
    /// Returns whether the file was adopted.
    @discardableResult
    public func adoptUploaded(_ file: UnregisteredFile, uploadedAs stamp: FileStamp, nodeID: String, version: Int64,
                              awaitingServerName: Bool = false) throws -> Bool {
        guard try isStillUnregistered(file), self.stamp(of: file.url) == stamp else { return false }
        try adopt(fileAt: file.url, nodeID: nodeID, version: version, relPath: file.relPath, awaitingServerName: awaitingServerName)
        return true
    }

    // What the rows claim: the paths of placed copies, and — for rows that could not be
    // placed — anything their recorded path may have meant.
    private struct Claims {
        let placed: Set<String>
        let unplaced: [String]
        init(_ store: LocalStore) throws {
            placed = try store.registeredRelPaths()
            unplaced = try store.dbQueue.read { db in try String.fetchAll(db, sql: "SELECT path FROM local WHERE rel_path = ''") }
                .map { "/" + LocalStore.key(IndexStore.normalize($0)) }
        }
        func cover(_ rel: String) -> Bool {
            let k = LocalStore.key(rel)
            return placed.contains(k) || unplaced.contains { $0.hasSuffix("/" + k) }
        }
    }

    fileprivate func registeredRelPaths() throws -> Set<String> {
        let rows = try dbQueue.read { db in try Row.fetchAll(db, sql: "SELECT path, rel_path FROM local") }
        return Set(rows.compactMap { row -> String? in
            let rel = row["rel_path"] as String
            return rel.isEmpty ? Self.relPath(of: URL(fileURLWithPath: row["path"] as String), under: contentDir) : rel
        }.map(Self.key))
    }

    // One file, one key: the same name composed or decomposed is the same file on disk.
    fileprivate static func key(_ relPath: String) -> String { relPath.precomposedStringWithCanonicalMapping }

    // Where copies recorded by absolute path sit under `root` now. Under it still: the rest
    // of the path. Not under it — the container moved before relative paths were kept. The
    // content directory kept its name and the tree below it moved whole, so the old root
    // ends in that name; but the name may occur in a path more than once, and a wrong
    // guess hands a row some file of the user's, for "Free up space" to delete. The old
    // root is therefore taken only when it is the one place every such path can have been
    // under; otherwise nothing is recovered. A row is placed only on a file that is there.
    static func recoveredRelPaths(ofRecorded rows: [(id: String, path: String)], under root: URL) -> [(id: String, rel: String)] {
        var out: [(id: String, rel: String)] = []
        var moved: [(id: String, parts: [String])] = []
        for row in rows {
            if let rel = relPath(of: URL(fileURLWithPath: row.path), under: root) { out.append((row.id, rel)) }
            else { moved.append((row.id, row.path.split(separator: "/").map(String.init))) }
        }
        guard !moved.isEmpty else { return out }
        let name = root.lastPathComponent
        func roots(_ parts: [String]) -> Set<[String]> {
            Set(parts.indices.dropLast().filter { parts[$0] == name }.map { Array(parts[...$0]) })
        }
        let common = moved.dropFirst().reduce(roots(moved[0].parts)) { $0.intersection(roots($1.parts)) }
        guard common.count == 1, let oldRoot = common.first else { return out }
        for row in moved {
            let rel = sanitized(row.parts.dropFirst(oldRoot.count).joined(separator: "/"))
            let candidate = rel.split(separator: "/").reduce(root) { $0.appendingPathComponent(String($1)) }
            if !rel.isEmpty, FileManager.default.fileExists(atPath: candidate.path) { out.append((row.id, rel)) }
        }
        return out
    }

    // Whether two paths name one file. Strings do not say: on a case-insensitive volume
    // "file.txt" and "FILE.txt" are the same file.
    static func sameFile(_ a: URL, _ b: URL) -> Bool {
        if a.standardizedFileURL.path == b.standardizedFileURL.path { return true }
        guard let ia = (try? a.resourceValues(forKeys: [.fileResourceIdentifierKey]))?.fileResourceIdentifier,
              let ib = (try? b.resourceValues(forKeys: [.fileResourceIdentifierKey]))?.fileResourceIdentifier else { return false }
        return ia.isEqual(ib)
    }

    // A file's path under `root` in the index's spelling, nil when it is not under it.
    // Symlinks are resolved on both sides: the enumerator hands back /private/var/… for a
    // root spelled /var/….
    static func relPath(of url: URL, under root: URL) -> String? {
        let base = root.resolvingSymlinksInPath().standardizedFileURL.path
        let path = url.resolvingSymlinksInPath().standardizedFileURL.path
        guard path.hasPrefix(base + "/") else { return nil }
        let rel = IndexStore.normalize(String(path.dropFirst(base.count)))
        return rel.isEmpty ? nil : rel
    }

    /// Registers a file the import just uploaded as the local copy of the node it became.
    /// The file moves to where that node's copy belongs when that differs from where it
    /// lies — an upload the server filed as a conflict copy has another name — unless
    /// something is already there; it is registered wherever it ends up.
    ///
    /// `awaitingServerName`: the upload became a conflict copy, whose name only the index
    /// will tell. The row remembers that it is owed one — see `settleName` — so a refresh
    /// that fails right after the upload does not leave the copy under the wrong name for good.
    public func adopt(fileAt url: URL, nodeID: String, version: Int64, relPath: String, awaitingServerName: Bool = false) throws {
        let fm = FileManager.default
        guard fm.fileExists(atPath: url.path), var rel = Self.relPath(of: url, under: contentDir) else { return }
        let wanted = Self.sanitized(relPath)
        if !wanted.isEmpty, Self.key(wanted) != Self.key(rel) {
            let dst = fileURL(forRelPath: wanted)
            if !fm.fileExists(atPath: dst.path) {
                try fm.createDirectory(at: dst.deletingLastPathComponent(), withIntermediateDirectories: true)
                try fm.moveItem(at: url, to: dst)
                pruneEmptyParents(of: url)
                rel = wanted
            }
        }
        try register(nodeID: nodeID, state: "cached", version: version, relPath: rel)
        try dbQueue.write { db in
            try db.execute(sql: "UPDATE local SET awaits_name = ? WHERE node_id = ?", arguments: [awaitingServerName, nodeID])
        }
    }

    /// The copies registered under a provisional name.
    public func copiesAwaitingServerName() throws -> [String] {
        try dbQueue.read { db in try String.fetchAll(db, sql: "SELECT node_id FROM local WHERE awaits_name = 1") }
    }

    /// Gives such a copy the name an up-to-date index has for its node; nil — the node is
    /// not there any more — leaves the copy where it is. Either way nothing is owed after.
    public func settleName(nodeID: String, serverPath: String?) throws {
        if let serverPath, let url = localURL(nodeID: nodeID), let version = try version(nodeID: nodeID) {
            try adopt(fileAt: url, nodeID: nodeID, version: version, relPath: serverPath)
        } else {
            try dbQueue.write { db in try db.execute(sql: "UPDATE local SET awaits_name = 0 WHERE node_id = ?", arguments: [nodeID]) }
        }
    }

    // Safely builds a path under contentDir from a server-relative path
    // (strips ".", "..", and empty segments to prevent directory traversal).
    private func fileURL(forRelPath relPath: String) -> URL {
        Self.sanitized(relPath).split(separator: "/").reduce(contentDir) { $0.appendingPathComponent(String($1)) }
    }

    private static func sanitized(_ relPath: String) -> String {
        IndexStore.normalize(relPath).split(separator: "/").filter { $0 != "." && $0 != ".." }.joined(separator: "/")
    }

    // One row per node and one node per local path. A pin outlives the copy it was put
    // on: a newer version stored over a pinned copy is pinned too.
    private func register(nodeID: String, state: String, version: Int64, relPath: String) throws {
        try dbQueue.write { db in
            try db.execute(sql: "DELETE FROM local WHERE rel_path = ? AND node_id != ?", arguments: [relPath, nodeID])
            try db.execute(sql: """
                INSERT INTO local(node_id,state,version,path,rel_path) VALUES(?,?,?,?,?)
                ON CONFLICT(node_id) DO UPDATE SET
                  state = CASE WHEN local.state = 'pinned' THEN 'pinned' ELSE excluded.state END,
                  version=excluded.version, path=excluded.path, rel_path=excluded.rel_path
            """, arguments: [nodeID, state, version, fileURL(forRelPath: relPath).path, relPath])
        }
    }

    // relPath — full path from the root (mirrors the server tree, giving sensible names in Finder).
    public func store(nodeID: String, version: Int64, from tmp: URL, pinned: Bool, relPath: String) throws {
        let rel = Self.sanitized(relPath)
        let dst = fileURL(forRelPath: rel)
        let previous = registeredURL(nodeID: nodeID)
        try FileManager.default.createDirectory(at: dst.deletingLastPathComponent(), withIntermediateDirectories: true)
        if FileManager.default.fileExists(atPath: dst.path) { try FileManager.default.removeItem(at: dst) }
        // Claimed before it appears: an import scanning the folder never finds the file
        // without its row. A row whose file did not arrive reads as "not downloaded".
        try register(nodeID: nodeID, state: pinned ? "pinned" : "cached", version: version, relPath: rel)
        try FileManager.default.moveItem(at: tmp, to: dst)
        DownloadQuarantine.mark(dst)
        // Renamed or moved on the server: the copy under the old name goes, or it would
        // sit there unclaimed and look like a file the user added.
        // Unless the old name is the new file: a change of case only, on a volume that
        // does not tell them apart.
        if let previous, FileManager.default.fileExists(atPath: previous.path), !Self.sameFile(previous, dst) {
            try? FileManager.default.removeItem(at: previous)
            pruneEmptyParents(of: previous)
        }
    }

    // The server version the registered copy is, nil when there is none.
    public func version(nodeID: String) throws -> Int64? {
        try dbQueue.read { db in try Int64.fetchOne(db, sql: "SELECT version FROM local WHERE node_id=?", arguments: [nodeID]) }
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
