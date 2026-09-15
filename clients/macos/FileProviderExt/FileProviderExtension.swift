import AppKit
import FileProvider
import DiscoKit

// The DiscoDrive folder in Finder. Reads come from the shared index and the server; writes
// go to the server through the same calls the app uses, then the index is pulled so the
// returned item carries the server's id and version.
final class FileProviderExtension: NSObject, NSFileProviderReplicatedExtension, NSFileProviderCustomAction, @unchecked Sendable {   // immutable after init
    let domain: NSFileProviderDomain
    private let core: ProviderCore?
    // Set when this domain is an unlocked vault rather than the storage itself.
    private let vaultCore: VaultCore?

    required init(domain: NSFileProviderDomain) {
        self.domain = domain
        let core = ProviderCore()
        self.core = core
        self.vaultCore = core.flatMap { VaultCore(core: $0, domain: domain) }
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
        var url: URL?
        switch actionIdentifier {
        case Self.openVaultAction:
            if let id = itemIdentifiers.first?.rawValue { url = URL(string: "discodrive://vault/open?id=\(id)") }
        case Self.closeVaultAction:
            if let vc = vaultCore { url = URL(string: "discodrive://vault/close?id=\(vc.vaultID)") }
        default: break
        }
        if let url { NSWorkspace.shared.open(url) }
        completionHandler(nil)
        return Progress()
    }

    func invalidate() {}

    private func requireCore() throws -> ProviderCore {
        guard let core else { throw NSFileProviderError(.notAuthenticated) }
        return core
    }

    func item(for identifier: NSFileProviderItemIdentifier, request: NSFileProviderRequest,
              completionHandler: @escaping (NSFileProviderItem?, Error?) -> Void) -> Progress {
        if let vc = vaultCore {
            nonisolated(unsafe) let completionHandler = completionHandler
            Task {
                do {
                    guard let id = VaultItemID.decode(identifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    completionHandler(try await vc.item(for: id), nil)
                } catch { completionHandler(nil, error) }
            }
            return Progress()
        }
        do {
            if identifier == .rootContainer { completionHandler(RootItem(), nil); return Progress() }
            let core = try requireCore()
            if let node = try core.index.node(id: identifier.rawValue) {
                completionHandler(core.item(for: node), nil)
            } else {
                completionHandler(nil, NSFileProviderError(.noSuchItem))
            }
        } catch {
            completionHandler(nil, error)
        }
        return Progress()
    }

    func fetchContents(for itemIdentifier: NSFileProviderItemIdentifier, version requestedVersion: NSFileProviderItemVersion?,
                       request: NSFileProviderRequest,
                       completionHandler: @escaping (URL?, NSFileProviderItem?, Error?) -> Void) -> Progress {
        let progress = Progress(totalUnitCount: 1)
        // The completion handler is the system's; calling it from the task's thread is fine.
        nonisolated(unsafe) let completionHandler = completionHandler
        let task = Task<Void, Never> {
            do {
                if let vc = vaultCore {
                    guard let id = VaultItemID.decode(itemIdentifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    let url = try await vc.decrypt(id)
                    completionHandler(url, try await vc.item(for: id), nil)
                    return
                }
                let core = try requireCore()
                guard let node = try core.index.node(id: itemIdentifier.rawValue) else {
                    throw NSFileProviderError(.noSuchItem)
                }
                let url = try await core.download(nodeID: node.id)
                // The bytes belong to whatever the index says now; if the server moved on
                // meanwhile the item's version tells the system to fetch again.
                let current = try core.index.node(id: node.id) ?? node
                completionHandler(url, core.item(for: current), nil)
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
        if let vc = vaultCore { return VaultEnumerator(vc: vc, container: containerItemIdentifier) }
        if domain.identifier.rawValue.hasPrefix(VaultCore.domainPrefix) {
            // A vault domain whose keys are gone (closed, or the app quit): nothing to show.
            throw NSFileProviderError(.notAuthenticated)
        }
        return Enumerator(core: try requireCore(), container: containerItemIdentifier)
    }

    // MARK: - Writes

    func createItem(basedOn itemTemplate: NSFileProviderItem, fields: NSFileProviderItemFields, contents url: URL?,
                    options: NSFileProviderCreateItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        nonisolated(unsafe) let completionHandler = completionHandler
        nonisolated(unsafe) let template = itemTemplate
        let progress = Progress(totalUnitCount: 1)
        let task = Task<Void, Never> {
            do {
                // Finder's own housekeeping files stay on this Mac; the system keeps them
                // without asking again.
                if LocalOnlyNames.isLocalOnly(template.filename) { throw NSFileProviderError(.excludedFromSync) }
                if let vc = vaultCore {
                    let parent = try vc.dirID(of: template.parentItemIdentifier)
                    let item = template.contentType == .folder
                        ? try await vc.createFolder(name: template.filename, in: parent)
                        : try await vc.createFile(name: template.filename, contents: try url ?? Self.emptyFile(), in: parent)
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
                if let existing = try core.index.node(atPath: path) {
                    ProviderCore.log.error("createItem for an existing path \(path, privacy: .public): returning the server's item (contents \(url == nil ? "none" : "offered", privacy: .public), fields \(fields.rawValue))")
                    completionHandler(core.item(for: existing), [], false, nil)
                    return
                }
                if template.contentType == .folder {
                    try await core.createFolder(path: path)
                } else if let url {
                    _ = try await core.upload(fileURL: url, path: path, baseVersion: nil)
                } else if fields.contains(.contents) {
                    // Contents were promised but not handed over: nothing to put on the server.
                    throw NSFileProviderError(.noSuchItem)
                } else {
                    // A file with no contents yet is created empty, as Finder's "New Document" does.
                    _ = try await core.upload(fileURL: try Self.emptyFile(), path: path, baseVersion: nil)
                }
                let node = try await core.pullAndFind(path: path)
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
        let progress = Progress(totalUnitCount: 1)
        let task = Task<Void, Never> {
            do {
                if LocalOnlyNames.isLocalOnly(item.filename) { throw NSFileProviderError(.excludedFromSync) }
                if let vc = vaultCore {
                    guard let id = VaultItemID.decode(item.itemIdentifier.rawValue) else { throw NSFileProviderError(.noSuchItem) }
                    var current: VaultItem? = nil
                    if changedFields.contains(.parentItemIdentifier) {
                        current = try await vc.move(id, to: item.parentItemIdentifier, as: item.filename)
                    } else if changedFields.contains(.filename) {
                        current = try await vc.rename(id, to: item.filename)
                    }
                    if changedFields.contains(.contents), let newContents {
                        current = try await vc.replaceContents(of: (current?.id ?? id), with: newContents)
                    }
                    if current == nil { current = try await vc.item(for: id) }
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
                    // The version Finder edited from guards the upload: a newer server
                    // version is kept and this one filed as a conflict copy next to it,
                    // in which case Finder is told to fetch the server's file again.
                    let outcome = try await core.upload(fileURL: newContents, path: node.path, baseVersion: node.version)
                    fetchAgain = outcome.conflicted
                    node = try await core.pullAndFind(path: node.path)
                }
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
                if let vc = vaultCore {
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
