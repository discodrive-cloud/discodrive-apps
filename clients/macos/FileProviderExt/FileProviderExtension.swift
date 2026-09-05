import FileProvider
import DiscoKit

// The DiscoDrive folder in Finder. Reads come from the shared index and the server; every
// write is refused for now (that is the next phase), so Finder offers none.
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
                completionHandler(ProviderItem(node: node), nil)
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
                completionHandler(url, ProviderItem(node: current), nil)
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

    // MARK: - Writes: not yet

    private static let readOnly = NSError(domain: NSCocoaErrorDomain, code: NSFeatureUnsupportedError)

    func createItem(basedOn itemTemplate: NSFileProviderItem, fields: NSFileProviderItemFields, contents url: URL?,
                    options: NSFileProviderCreateItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        completionHandler(nil, [], false, Self.readOnly)
        return Progress()
    }

    func modifyItem(_ item: NSFileProviderItem, baseVersion version: NSFileProviderItemVersion,
                    changedFields: NSFileProviderItemFields, contents newContents: URL?,
                    options: NSFileProviderModifyItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        completionHandler(nil, [], false, Self.readOnly)
        return Progress()
    }

    func deleteItem(identifier: NSFileProviderItemIdentifier, baseVersion version: NSFileProviderItemVersion,
                    options: NSFileProviderDeleteItemOptions, request: NSFileProviderRequest,
                    completionHandler: @escaping (Error?) -> Void) -> Progress {
        completionHandler(Self.readOnly)
        return Progress()
    }
}
