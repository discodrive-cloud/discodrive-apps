#if os(macOS)
import AppKit
#endif
import FileProvider
import DiscoKit

// The DiscoDrive folder in Finder. Reads come from the shared index and the server; writes
// go to the server through the same calls the app uses, then the index is pulled so the
// returned item carries the server's id and version.
final class FileProviderExtension: NSObject, NSFileProviderReplicatedExtension, NSFileProviderCustomAction, @unchecked Sendable {   // mutable core is protected by coreLock
    let domain: NSFileProviderDomain
    private let coreLock = NSLock()
    private var cachedCore: ProviderCore?
    private let makeCore: @Sendable () throws -> ProviderCore

    required convenience init(domain: NSFileProviderDomain) {
        self.init(domain: domain, makeCore: { try ProviderCore() })
    }

    init(domain: NSFileProviderDomain, makeCore: @escaping @Sendable () throws -> ProviderCore) {
        self.domain = domain
        self.makeCore = makeCore
        super.init()
    }

    // MARK: - Context menu actions

    static let openVaultAction = NSFileProviderExtensionActionIdentifier("org.discodrive.openVault")
    static let closeVaultAction = NSFileProviderExtensionActionIdentifier("org.discodrive.closeVault")

    // The extension has no window: both actions hand the app a URL, and the app asks for
    // the password or drops the domain.
    func performAction(identifier actionIdentifier: NSFileProviderExtensionActionIdentifier,
                       onItemsWithIdentifiers itemIdentifiers: [NSFileProviderItemIdentifier],
                       completionHandler: @escaping (Error?) -> Void) -> Progress {
        let scheme = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveURLScheme") as? String ?? "discodrive"
        var url: URL?
        do {
            switch actionIdentifier {
            case Self.openVaultAction:
                if let id = itemIdentifiers.first?.rawValue { url = URL(string: "\(scheme)://vault/open?id=\(id)") }
            case Self.closeVaultAction:
                if let vc = try requireVaultCore() { url = URL(string: "\(scheme)://vault/close?id=\(vc.vaultID)") }
            default: break
            }
        } catch { completionHandler(error); return Progress() }
        #if os(macOS)
        if let url { NSWorkspace.shared.open(url) }
        #else
        if url != nil { completionHandler(NSError(domain: NSCocoaErrorDomain, code: NSFeatureUnsupportedError)); return Progress() }
        #endif
        completionHandler(nil)
        return Progress()
    }

    func invalidate() {}

    private func requireCore() throws -> ProviderCore {
        coreLock.lock(); defer { coreLock.unlock() }
        if let cachedCore { return cachedCore }
        // A locked keychain or unavailable container at launch must not poison this
        // extension instance forever. Failed initialization is retried on the next call.
        let core = try makeCore()
        cachedCore = core
        return core
    }

    private func requireVaultCore() throws -> VaultCore? {
        guard domain.identifier.rawValue.hasPrefix(VaultCore.domainPrefix) else { return nil }
        guard let core = try VaultCore(core: try requireCore(), domain: domain) else {
            throw NSFileProviderError(.notAuthenticated)
        }
        return core
    }

    func item(for identifier: NSFileProviderItemIdentifier, request: NSFileProviderRequest,
              completionHandler: @escaping (NSFileProviderItem?, Error?) -> Void) -> Progress {
        nonisolated(unsafe) let completionHandler = completionHandler
        Task {
            do {
                if let vc = try requireVaultCore() {
                    guard let id = VaultItemID.decode(identifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    completionHandler(try await vc.item(for: id), nil)
                    return
                }
                if identifier == .rootContainer { completionHandler(RootItem(), nil); return }
                let core = try requireCore()
                guard let node = try core.index.node(id: identifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                completionHandler(core.item(for: node), nil)
            } catch { completionHandler(nil, error) }
        }
        return Progress()
    }

    func fetchContents(for itemIdentifier: NSFileProviderItemIdentifier, version requestedVersion: NSFileProviderItemVersion?,
                       request: NSFileProviderRequest,
                       completionHandler: @escaping (URL?, NSFileProviderItem?, Error?) -> Void) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        progress.kind = .file
        progress.fileOperationKind = .downloading
        // The completion handler is the system's; calling it from the task's thread is fine.
        nonisolated(unsafe) let completionHandler = completionHandler
        let task = Task<Void, Never> {
            do {
                if let vc = try requireVaultCore() {
                    guard let id = VaultItemID.decode(itemIdentifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    let url = try await vc.decrypt(id)
                    let item = try await vc.item(for: id)
                    progress.completedUnitCount = progress.totalUnitCount
                    completionHandler(url, item, nil)
                    return
                }
                let core = try requireCore()
                guard let node = try core.index.node(id: itemIdentifier.rawValue) else {
                    throw NSFileProviderError(.noSuchItem)
                }
                // The item names the version the bytes really are — it comes back as the
                // base of the next edit — not whatever the index holds by now.
                progress.totalUnitCount = max(1, node.size)
                let (url, item) = try await core.fetch(node, progress: { received, expected in
                    let total = max(1, expected > 0 ? expected : node.size, received)
                    progress.totalUnitCount = total
                    // Keep completion for the point where the file has been validated
                    // and the correct item version is ready to hand back to Finder.
                    progress.completedUnitCount = min(max(0, received), total - 1)
                })
                progress.completedUnitCount = progress.totalUnitCount
                completionHandler(url, item, nil)
            } catch is CancellationError {
                completionHandler(nil, nil, NSError(domain: NSCocoaErrorDomain, code: NSUserCancelledError))
            } catch {
                completionHandler(nil, nil, error)
            }
        }
        progress.cancellationHandler = { task.cancel() }
        return progress
    }

    func enumerator(for containerItemIdentifier: NSFileProviderItemIdentifier,
                    request: NSFileProviderRequest) throws -> NSFileProviderEnumerator {
        if let vc = try requireVaultCore() { return VaultEnumerator(vc: vc, container: containerItemIdentifier) }
        return Enumerator(core: try requireCore(), container: containerItemIdentifier)
    }

    // MARK: - Writes

    func createItem(basedOn itemTemplate: NSFileProviderItem, fields: NSFileProviderItemFields, contents url: URL?,
                    options: NSFileProviderCreateItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        nonisolated(unsafe) let completionHandler = completionHandler
        nonisolated(unsafe) let template = itemTemplate
        let progress = Progress(totalUnitCount: 1)
        progress.kind = .file
        progress.fileOperationKind = .uploading
        let report: @Sendable (Int64, Int64) -> Void = { sent, total in
            progress.totalUnitCount = max(1, total, sent)
            progress.completedUnitCount = min(max(0, sent), progress.totalUnitCount - 1)
        }
        let task = Task<Void, Never> {
            do {
                // Finder's own housekeeping files stay on this Mac; the system keeps them
                // without asking again.
                if LocalOnlyNames.isLocalOnly(template.filename) { throw NSFileProviderError(.excludedFromSync) }
                if let vc = try requireVaultCore() {
                    let parent = try vc.dirID(of: template.parentItemIdentifier)
                    // As below for the storage itself: a name the vault already holds is
                    // answered with that entry, never written over.
                    if let existing = try await vc.existingItem(name: template.filename, in: parent) {
                        ProviderCore.log.error("createItem for an existing vault entry: returning it (contents \(url == nil ? "none" : "offered", privacy: .public), fields \(fields.rawValue))")
                        progress.completedUnitCount = progress.totalUnitCount
                        completionHandler(existing, [], false, nil)
                        return
                    }
                    let item = template.contentType == .folder
                        ? try await vc.createFolder(name: template.filename, in: parent)
                        : try await vc.createFile(name: template.filename, contents: try url ?? Self.emptyFile(), in: parent)
                    progress.completedUnitCount = progress.totalUnitCount
                    completionHandler(item, [], false, nil)
                    return
                }
                let core = try requireCore()
                let folder = try core.folderPath(template.parentItemIdentifier)
                if try core.index.isInsideVault(path: folder) { throw NSFileProviderError(.noSuchItem) }
                let path = IndexStore.path(in: folder, name: template.filename)
                // The system also asks to "create" what it finds on disk but cannot match to
                // an item, as after a reimport: for a path the server already has, the answer
                // is that item — never a fresh upload over it, and never an empty one.
                _ = try await core.pull(since: try core.index.cursor())
                if let existing = try core.index.node(atPath: path) {
                    ProviderCore.log.error("createItem for an existing path \(path, privacy: .public): returning the server's item (contents \(url == nil ? "none" : "offered", privacy: .public), fields \(fields.rawValue))")
                    progress.completedUnitCount = progress.totalUnitCount
                    completionHandler(core.item(for: existing), [], false, nil)
                    return
                }
                // The index may be behind: another client can have taken the name since.
                // A new file therefore goes up as "create only if absent" (base version 0
                // matches no existing file) — the server then keeps theirs and files this
                // one beside it as a conflict copy, which is the item Finder gets back.
                var outcome: APIClient.UploadOutcome?
                if template.contentType == .folder {
                    try await core.createFolder(path: path)   // idempotent on the server
                } else if let url {
                    outcome = try await core.upload(fileURL: url, path: path, baseVersion: ContentVersionCodec.unknownBase, progress: report)
                } else if fields.contains(.contents) {
                    // Contents were promised but not handed over: nothing to put on the server.
                    throw NSFileProviderError(.noSuchItem)
                } else {
                    // A file with no contents yet is created empty, as Finder's "New Document" does.
                    outcome = try await core.upload(fileURL: try Self.emptyFile(), path: path, baseVersion: ContentVersionCodec.unknownBase, progress: report)
                }
                if outcome?.conflicted == true {
                    ProviderCore.log.error("createItem: \(path, privacy: .public) was taken meanwhile; kept as a conflict copy")
                }
                var node = try await core.pullAndFind(path: path)
                if let id = outcome?.nodeID, !id.isEmpty, id != node.id, let own = try core.index.node(id: id) { node = own }
                progress.completedUnitCount = progress.totalUnitCount
                completionHandler(core.item(for: node), [], false, nil)
            } catch {
                completionHandler(nil, [], false, error)
            }
        }
        progress.cancellationHandler = { task.cancel() }
        return progress
    }

    func modifyItem(_ item: NSFileProviderItem, baseVersion version: NSFileProviderItemVersion,
                    changedFields: NSFileProviderItemFields, contents newContents: URL?,
                    options: NSFileProviderModifyItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        nonisolated(unsafe) let completionHandler = completionHandler
        nonisolated(unsafe) let item = item
        let editedFrom = version.contentVersion   // what the system says the new contents are based on
        let progress = Progress(totalUnitCount: 1)
        progress.kind = .file
        progress.fileOperationKind = .uploading
        let report: @Sendable (Int64, Int64) -> Void = { sent, total in
            progress.totalUnitCount = max(1, total, sent)
            progress.completedUnitCount = min(max(0, sent), progress.totalUnitCount - 1)
        }
        let task = Task<Void, Never> {
            do {
                if LocalOnlyNames.isLocalOnly(item.filename) { throw NSFileProviderError(.excludedFromSync) }
                if let vc = try requireVaultCore() {
                    guard let id = VaultItemID.decode(item.itemIdentifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    if changedFields.contains(.contents),
                       changedFields.contains(.filename) || changedFields.contains(.parentItemIdentifier) {
                        let existing = try await vc.item(for: id)
                        let old = ContentVersionCodec.decode(editedFrom)
                        let now = ContentVersionCodec.decode(existing.itemVersion.contentVersion)
                        guard old?.hash == now?.hash, old?.hash.isEmpty == false else { throw NSFileProviderError(.cannotSynchronize) }
                    }
                    var current: VaultItem? = nil
                    if changedFields.contains(.parentItemIdentifier) {
                        current = try await vc.move(id, to: item.parentItemIdentifier, as: item.filename)
                    } else if changedFields.contains(.filename) {
                        current = try await vc.rename(id, to: item.filename)
                    }
                    if changedFields.contains(.contents), let newContents {
                        current = try await vc.replaceContents(of: (current?.id ?? id), with: newContents, editedFrom: current?.itemVersion.contentVersion ?? editedFrom)
                    }
                    if current == nil { current = try await vc.item(for: id) }
                    progress.completedUnitCount = progress.totalUnitCount
                    completionHandler(current, [], false, nil)
                    return
                }
                let core = try requireCore()
                guard var node = try core.index.node(id: item.itemIdentifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                if try core.index.isInsideVault(path: node.path) { throw NSFileProviderError(.noSuchItem) }
                // Name and place first, so new contents go to where the file now lives.
                if changedFields.contains(.parentItemIdentifier) {
                    let parent = item.parentItemIdentifier
                    if try core.index.isInsideVault(path: try core.folderPath(parent)) { throw NSFileProviderError(.noSuchItem) }
                    try await core.move(nodeID: node.id, toParent: parent == .rootContainer ? nil : parent.rawValue)
                }
                if changedFields.contains(.filename), item.filename != node.name {
                    try await core.rename(nodeID: node.id, to: item.filename)
                }
                if changedFields.contains(.parentItemIdentifier) || changedFields.contains(.filename) {
                    let folder = try core.folderPath(item.parentItemIdentifier)
                    node = try await core.pullAndFind(path: IndexStore.path(in: folder, name: item.filename))
                }
                var fetchAgain = false
                if changedFields.contains(.contents), let newContents, !node.isDir {
                    // The version Finder edited from — the base it hands over, not the
                    // index's current one — guards the upload: a newer server version is
                    // kept and this one filed as a conflict copy next to it, in which case
                    // Finder is told to fetch the server's file again.
                    let base = ContentVersionCodec.uploadBase(editedFrom: editedFrom, current: node)
                    let outcome = try await core.upload(fileURL: newContents, path: node.path, baseVersion: base, progress: report)
                    fetchAgain = outcome.conflicted
                    node = try await core.pullAndFind(path: node.path)
                }
                progress.completedUnitCount = progress.totalUnitCount
                completionHandler(core.item(for: node), [], fetchAgain, nil)
            } catch {
                completionHandler(nil, [], false, error)
            }
        }
        progress.cancellationHandler = { task.cancel() }
        return progress
    }

    func deleteItem(identifier: NSFileProviderItemIdentifier, baseVersion version: NSFileProviderItemVersion,
                    options: NSFileProviderDeleteItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (Error?) -> Void) -> Progress {
        nonisolated(unsafe) let completionHandler = completionHandler
        let progress = Progress(totalUnitCount: 1)
        let task = Task<Void, Never> {
            do {
                if let vc = try requireVaultCore() {
                    guard let id = VaultItemID.decode(identifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    do { try await vc.delete(id) } catch let e as NSFileProviderError where e.code == .noSuchItem {}   // already gone
                    completionHandler(nil)
                    return
                }
                let core = try requireCore()
                guard let node = try core.index.node(id: identifier.rawValue) else {
                    completionHandler(nil)   // already gone: that is the outcome asked for
                    return
                }
                if try core.index.isInsideVault(path: node.path) { throw NSFileProviderError(.noSuchItem) }
                // The server keeps a trash with versions, so this is recoverable there.
                try await core.delete(nodeID: node.id)
                _ = try await core.pull(since: try core.index.cursor())
                completionHandler(nil)
            } catch {
                completionHandler(error)
            }
        }
        progress.cancellationHandler = { task.cancel() }
        return progress
    }

    private static func emptyFile() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try Data().write(to: url)
        return url
    }
}
