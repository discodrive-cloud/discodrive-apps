import FileProvider
import DiscoKit

// The DiscoDrive folder in Finder. Reads come from the shared index and the server; writes
// go to the server through the same calls the app uses, then the index is pulled so the
// returned item carries the server's id and version.
final class FileProviderExtension: NSObject, NSFileProviderReplicatedExtension, @unchecked Sendable {   // immutable after init
    let domain: NSFileProviderDomain
    private let core: ProviderCore?

    required init(domain: NSFileProviderDomain) {
        self.domain = domain
        self.core = ProviderCore()
        super.init()
    }

    func invalidate() {}

    private func requireCore() throws -> ProviderCore {
        guard let core else { throw NSFileProviderError(.notAuthenticated) }
        return core
    }

    func item(for identifier: NSFileProviderItemIdentifier, request: NSFileProviderRequest,
              completionHandler: @escaping (NSFileProviderItem?, Error?) -> Void) -> Progress {
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
        Enumerator(core: try requireCore(), container: containerItemIdentifier)
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
                let core = try requireCore()
                let folder = try core.folderPath(template.parentItemIdentifier)
                if try core.index.isInsideVault(path: folder) { throw NSFileProviderError(.noSuchItem) }
                let path = IndexStore.path(in: folder, name: template.filename)
                if template.contentType == .folder {
                    try await core.createFolder(path: path)
                } else {
                    // A file with no contents yet is created empty, as Finder's "New Document" does.
                    let src = try url ?? Self.emptyFile()
                    _ = try await core.upload(fileURL: src, path: path, baseVersion: nil)
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
