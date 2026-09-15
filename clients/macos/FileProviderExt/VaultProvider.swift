import FileProvider
import UniformTypeIdentifiers
import DiscoKit

// An unlocked Cryptomator vault as its own Finder location. The app unlocked it and left
// the keys in the shared keychain; this reads the ciphertext through the same index and
// server client as the main domain and shows the cleartext tree.
final class VaultCore: @unchecked Sendable {
    let core: ProviderCore
    let vaultID: String        // node id of the vault folder in the main tree
    let vaultName: String
    let vaultRoot: String      // server path of the vault folder
    let vault: Vault
    let io: ServerVaultIO

    static let domainPrefix = "vault-"

    init?(core: ProviderCore, domain: NSFileProviderDomain) {
        let raw = domain.identifier.rawValue
        guard raw.hasPrefix(Self.domainPrefix) else { return nil }
        let id = String(raw.dropFirst(Self.domainPrefix.count))
        guard let keys = VaultKeyStore.load(forVault: id), let vault = try? Vault(rawKeys: keys),
              let node = try? core.index.node(id: id) else { return nil }
        self.core = core
        self.vaultID = id
        self.vaultName = node.name
        self.vaultRoot = node.path
        self.vault = vault
        self.io = ServerVaultIO(vaultRoot: node.path, index: core.index, client: core.client)
    }

    // The ciphertext node behind an entry (its name.c9r file, or the .c9s wrapper folder).
    func node(for entry: VaultEntry) throws -> Node? {
        try core.index.node(atPath: vaultRoot + "/" + entry.encPath)
    }

    // Lists a directory and records what it learns about subdirectories, so a later
    // item(for:) after a restart can still place them.
    func entries(in dirID: String) async throws -> [(VaultEntry, Node)] {
        var out: [(VaultEntry, Node)] = []
        for e in try await vault.listEntries(dirID: dirID, source: io) {
            guard let n = try node(for: e) else { continue }
            if e.isDir, let sub = e.dirID {
                try core.index.rememberVaultDir(vault: vaultID, dirID: sub, parentDirID: dirID, name: e.name, entryNodeID: n.id)
            }
            out.append((e, n))
        }
        return out
    }

    func item(for id: VaultItemID) async throws -> VaultItem {
        switch id {
        case .root:
            return VaultItem.root(name: vaultName)
        case .dir(let dirID):
            guard let d = try core.index.vaultDir(vault: vaultID, dirID: dirID),
                  let n = try core.index.node(id: d.entryNodeID) else { throw NSFileProviderError(.noSuchItem) }
            return VaultItem(id: id, parent: d.parentDirID.isEmpty ? .root : .dir(dirID: d.parentDirID),
                             name: d.name, isDir: true, size: 0, version: n.version, contentHash: "dir")
        case .file(let parentDirID, let nodeID):
            guard let (e, n) = try await entries(in: parentDirID).first(where: { $0.1.id == nodeID }) else {
                throw NSFileProviderError(.noSuchItem)
            }
            return VaultItem(entry: e, node: n, parentDirID: parentDirID)
        }
    }

    // The entry a file item names, looked up in its directory.
    func entry(for id: VaultItemID) async throws -> (VaultEntry, Node) {
        guard case .file(let parentDirID, let nodeID) = id,
              let hit = try await entries(in: parentDirID).first(where: { $0.1.id == nodeID }) else {
            throw NSFileProviderError(.noSuchItem)
        }
        return hit
    }

    func decrypt(_ id: VaultItemID) async throws -> URL {
        let (e, _) = try await entry(for: id)
        guard let cp = e.contentPath else { throw NSFileProviderError(.noSuchItem) }
        let data = try await core.mapErrors { try await vault.decryptFile(at: cp, source: io) }
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try data.write(to: tmp)
        return tmp
    }

    // MARK: - Writes
    //
    // Every write goes through VaultWrite against the server, then the index is pulled and
    // the entry found again at the ciphertext path VaultWrite used, so the returned item
    // carries the server's node id and version. No base-version guard yet: the vault's
    // ciphertext is written by name, a concurrent edit elsewhere is overwritten.

    private func entryItem(name: String, parentDirID: String) async throws -> VaultItem {
        let node = try await core.pullAndFind(path: vaultRoot + "/" + vault.entryPath(name: name, parentDirID: parentDirID))
        guard let (e, n) = try await entries(in: parentDirID).first(where: { $0.1.id == node.id }) else {
            throw NSFileProviderError(.noSuchItem)
        }
        return VaultItem(entry: e, node: n, parentDirID: parentDirID)
    }

    // The directory id a container identifier names; the root is "".
    func dirID(of container: NSFileProviderItemIdentifier) throws -> String {
        switch VaultItemID.decode(container.rawValue) {
        case .root?: return ""
        case .dir(let d)?: return d
        default: throw NSFileProviderError(.noSuchItem)
        }
    }

    func createFile(name: String, contents: URL, in parentDirID: String) async throws -> VaultItem {
        let data = try Data(contentsOf: contents)
        try await core.mapErrors { try await vault.addFile(name: name, data: data, parentDirID: parentDirID, sink: io) }
        return try await entryItem(name: name, parentDirID: parentDirID)
    }

    func createFolder(name: String, in parentDirID: String) async throws -> VaultItem {
        let sub = try await core.mapErrors { try await vault.createFolder(name: name, parentDirID: parentDirID, sink: io) }
        let item = try await entryItem(name: name, parentDirID: parentDirID)
        try core.index.rememberVaultDir(vault: vaultID, dirID: sub, parentDirID: parentDirID, name: name, entryNodeID: node(forEntryOf: item))
        return item
    }

    // The ciphertext node id behind a freshly listed item.
    private func node(forEntryOf item: VaultItem) -> String {
        if case .file(_, let nodeID) = item.id { return nodeID }
        return (try? core.index.node(atPath: vaultRoot + "/" + vault.entryPath(name: item.name, parentDirID: parentDirID(of: item))))?.id ?? ""
    }

    private func parentDirID(of item: VaultItem) -> String {
        if case .dir(let d) = item.parent { return d }
        return ""
    }

    // New contents for an existing file: written over the same name, the ciphertext node
    // gets a new version and keeps its id.
    func replaceContents(of id: VaultItemID, with contents: URL) async throws -> VaultItem {
        let (e, _) = try await entry(for: id)
        guard case .file(let parentDirID, _) = id else { throw NSFileProviderError(.noSuchItem) }
        let data = try Data(contentsOf: contents)
        try await core.mapErrors { try await vault.addFile(name: e.name, data: data, parentDirID: parentDirID, sink: io) }
        return try await entryItem(name: e.name, parentDirID: parentDirID)
    }

    // Rename in place; a file gets a new ciphertext node (VaultWrite re-encrypts it under
    // the new name), a directory keeps its id and storage.
    func rename(_ id: VaultItemID, to newName: String) async throws -> VaultItem {
        let (e, parentDirID) = try await entryAndParent(id)
        try await core.mapErrors { try await vault.renameEntry(e, to: newName, parentDirID: parentDirID, source: io, sink: io) }
        let item = try await entryItem(name: newName, parentDirID: parentDirID)
        if case .dir(let d) = id {
            try core.index.rememberVaultDir(vault: vaultID, dirID: d, parentDirID: parentDirID, name: newName, entryNodeID: node(forEntryOf: item))
        }
        return item
    }

    // Into another directory, with the name Finder shows at the end of the drag; a file
    // gets a new ciphertext node there, a directory keeps its id and storage.
    func move(_ id: VaultItemID, to newParent: NSFileProviderItemIdentifier, as newName: String) async throws -> VaultItem {
        let (e, parentDirID) = try await entryAndParent(id)
        let target = try dirID(of: newParent)
        try await core.mapErrors {
            try await vault.moveEntry(e, from: parentDirID, to: target, as: newName, source: io, sink: io)
        }
        let item = try await entryItem(name: newName, parentDirID: target)
        if case .dir(let d) = id {
            try core.index.rememberVaultDir(vault: vaultID, dirID: d, parentDirID: target, name: newName, entryNodeID: node(forEntryOf: item))
        }
        return item
    }

    func delete(_ id: VaultItemID) async throws {
        let (e, _) = try await entryAndParent(id)
        try await core.mapErrors { try await vault.deleteEntry(e, source: io, sink: io) }
        _ = try await core.pull(since: try core.index.cursor())
    }

    // The entry an item of either kind names, with the directory it lives in.
    private func entryAndParent(_ id: VaultItemID) async throws -> (VaultEntry, String) {
        switch id {
        case .file(let parentDirID, _):
            return (try await entry(for: id).0, parentDirID)
        case .dir(let dirID):
            guard let d = try core.index.vaultDir(vault: vaultID, dirID: dirID),
                  let e = try await vault.listEntries(dirID: d.parentDirID, source: io).first(where: { $0.dirID == dirID }) else {
                throw NSFileProviderError(.noSuchItem)
            }
            return (e, d.parentDirID)
        case .root:
            throw NSFileProviderError(.noSuchItem)
        }
    }

    // Which directories a batch of changed ciphertext paths touches: their storage folder
    // (d/XX/YYYY…) is the hash of their directory id, known for every directory seen so far.
    func dirIDs(touchedBy paths: [String]) throws -> Set<String> {
        var byStorage: [String: String] = ["d/" + vault.dirIdHash(""): ""]
        for d in try core.index.vaultDirs(vault: vaultID) { byStorage["d/" + vault.dirIdHash(d.dirID)] = d.dirID }
        var out = Set<String>()
        let prefix = vaultRoot + "/"
        for p in paths where p.hasPrefix(prefix) {
            let rel = String(p.dropFirst(prefix.count))
            for (storage, dirID) in byStorage where rel.hasPrefix(storage + "/") { out.insert(dirID) }
        }
        return out
    }
}

// Cryptomator's file layout: a 68-byte header, then the content in 32 KiB chunks each
// carrying 48 bytes of nonce and tag. Finder is told the cleartext size.
func vaultCleartextSize(ciphertext: Int64) -> Int64 {
    let header: Int64 = 68, chunk: Int64 = 32 * 1024, overhead: Int64 = 48
    guard ciphertext > header else { return 0 }
    let body = ciphertext - header
    let chunks = (body + chunk + overhead - 1) / (chunk + overhead)
    return max(0, body - chunks * overhead)
}

final class VaultItem: NSObject, NSFileProviderItem {
    let id: VaultItemID
    let parent: VaultItemID
    let name: String
    let isDir: Bool
    let size: Int64
    let version: Int64
    let contentHash: String

    init(id: VaultItemID, parent: VaultItemID, name: String, isDir: Bool, size: Int64, version: Int64, contentHash: String) {
        self.id = id; self.parent = parent; self.name = name; self.isDir = isDir
        self.size = size; self.version = version; self.contentHash = contentHash
    }

    convenience init(entry: VaultEntry, node: Node, parentDirID: String) {
        let parent: VaultItemID = parentDirID.isEmpty ? .root : .dir(dirID: parentDirID)
        if entry.isDir, let sub = entry.dirID {
            self.init(id: .dir(dirID: sub), parent: parent, name: entry.name, isDir: true, size: 0, version: node.version, contentHash: "dir")
        } else {
            self.init(id: .file(parentDirID: parentDirID, nodeID: node.id), parent: parent, name: entry.name, isDir: false,
                      size: vaultCleartextSize(ciphertext: node.size), version: node.version, contentHash: node.contentHash)
        }
    }

    static func root(name: String) -> VaultItem {
        VaultItem(id: .root, parent: .root, name: name, isDir: true, size: 0, version: 0, contentHash: "root")
    }

    var itemIdentifier: NSFileProviderItemIdentifier { .init(VaultItemID.encode(id)) }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .init(VaultItemID.encode(parent)) }
    var filename: String { name }
    var contentType: UTType {
        if isDir { return .folder }
        let ext = (name as NSString).pathExtension
        return ext.isEmpty ? .data : (UTType(filenameExtension: ext) ?? .data)
    }
    // No trash: a deleted entry is gone. Moves re-encrypt the entry under its new parent.
    var capabilities: NSFileProviderItemCapabilities {
        if id == .root { return [.allowsReading, .allowsContentEnumerating, .allowsAddingSubItems] }
        return isDir
            ? [.allowsReading, .allowsContentEnumerating, .allowsAddingSubItems, .allowsRenaming, .allowsReparenting, .allowsDeleting]
            : [.allowsReading, .allowsWriting, .allowsRenaming, .allowsReparenting, .allowsDeleting]
    }
    var contentPolicy: NSFileProviderContentPolicy { isDir ? .inherited : .downloadLazily }
    var documentSize: NSNumber? { isDir ? nil : NSNumber(value: size) }
    var itemVersion: NSFileProviderItemVersion {
        .init(contentVersion: Data(contentHash.utf8), metadataVersion: Data("\(version):\(name)".utf8))
    }
    // Lets the "Close vault" action show on anything inside an open vault.
    var userInfo: [AnyHashable: Any]? { ["vaultOpen": 1] }
}

final class VaultEnumerator: NSObject, NSFileProviderEnumerator, @unchecked Sendable {
    private let vc: VaultCore
    private let container: NSFileProviderItemIdentifier

    init(vc: VaultCore, container: NSFileProviderItemIdentifier) { self.vc = vc; self.container = container }
    func invalidate() {}

    private func listing(_ dirID: String) async throws -> [VaultItem] {
        try await vc.entries(in: dirID).map { VaultItem(entry: $0.0, node: $0.1, parentDirID: dirID) }
    }

    // The whole tree, for the working set.
    private func everything() async throws -> [VaultItem] {
        var out: [VaultItem] = []
        var queue = [""]
        while let dirID = queue.popLast() {
            for item in try await listing(dirID) {
                out.append(item)
                if case .dir(let sub) = item.id { queue.append(sub) }
            }
        }
        return out
    }

    func enumerateItems(for observer: NSFileProviderEnumerationObserver, startingAt page: NSFileProviderPage) {
        nonisolated(unsafe) let observer = observer
        Task {
            do {
                if try vc.core.index.cursor() == 0 { _ = try await vc.core.pull(since: 0) }
                let items: [VaultItem]
                switch container {
                case .workingSet: items = try await everything()
                case .rootContainer: items = try await listing("")
                case .trashContainer: items = []   // deletions are final in a vault
                default:
                    guard case .dir(let dirID)? = VaultItemID.decode(container.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    items = try await listing(dirID)
                }
                observer.didEnumerate(items)
                observer.finishEnumerating(upTo: nil)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func enumerateChanges(for observer: NSFileProviderChangeObserver, from anchor: NSFileProviderSyncAnchor) {
        nonisolated(unsafe) let observer = observer
        Task {
            do {
                let since = SyncAnchorCodec.decode(anchor.rawValue) ?? 0
                let delta = try await vc.core.pull(since: since)
                // Paths of what changed: still-present nodes from the index, gone ones we
                // cannot resolve any more, so a deletion re-lists every directory known.
                let changedPaths = delta.updated.compactMap { try? vc.core.index.node(id: $0)?.path }
                var dirs = try vc.dirIDs(touchedBy: changedPaths)
                if !delta.deleted.isEmpty { dirs.formUnion(try vc.core.index.vaultDirs(vault: vc.vaultID).map(\.dirID) + [""]) }
                var seen = Set<String>()
                for dirID in dirs {
                    let items = try await listing(dirID)
                    if !items.isEmpty { observer.didUpdate(items) }
                    seen.formUnion(items.map { VaultItemID.encode($0.id) })
                }
                if !delta.deleted.isEmpty {
                    // A file whose ciphertext node vanished: its id carries the node id.
                    let gone = delta.deleted.flatMap { n in dirs.map { VaultItemID.encode(.file(parentDirID: $0, nodeID: n)) } }
                    observer.didDeleteItems(withIdentifiers: gone.map(NSFileProviderItemIdentifier.init(_:)))
                }
                observer.finishEnumeratingChanges(upTo: NSFileProviderSyncAnchor(SyncAnchorCodec.encode(delta.cursor)), moreComing: false)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        completionHandler(NSFileProviderSyncAnchor(SyncAnchorCodec.encode((try? vc.core.index.cursor()) ?? 0)))
    }
}
