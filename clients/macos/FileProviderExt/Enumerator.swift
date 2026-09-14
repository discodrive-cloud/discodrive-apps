import FileProvider
import DiscoKit

// One enumerator serves both a folder (Finder listing its children) and the working set
// (the system asking what changed anywhere). Listing reads the shared index; change
// tracking asks the server for everything after the anchor, which is the index cursor.
final class Enumerator: NSObject, NSFileProviderEnumerator, @unchecked Sendable {   // immutable after init
    private let core: ProviderCore
    private let container: NSFileProviderItemIdentifier

    init(core: ProviderCore, container: NSFileProviderItemIdentifier) {
        self.core = core
        self.container = container
    }

    func invalidate() {}

    func enumerateItems(for observer: NSFileProviderEnumerationObserver, startingAt page: NSFileProviderPage) {
        // Observers are ObjC protocol objects the system hands over for exactly this call;
        // they are safe to answer from another thread, Swift just cannot prove it.
        nonisolated(unsafe) let observer = observer
        Task {
            do {
                // A fresh index (nothing applied yet) is filled from the beginning before
                // Finder gets its first, otherwise empty, answer.
                if try core.index.cursor() == 0 { _ = try await core.pull(since: 0) }
                let nodes: [Node]
                switch container {
                case .workingSet:    nodes = try core.index.allNodes()
                case .rootContainer: nodes = try core.index.children(of: nil)
                default:             nodes = try core.index.children(of: container.rawValue)
                }
                for chunk in stride(from: 0, to: nodes.count, by: 500) {
                    observer.didEnumerate(nodes[chunk..<min(chunk + 500, nodes.count)].map(core.item(for:)))
                }
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
                let delta = try await core.pull(since: since)
                let updated = delta.updated.compactMap { try? core.index.node(id: $0) }.map(core.item(for:))
                if !updated.isEmpty { observer.didUpdate(updated) }
                if !delta.deleted.isEmpty {
                    observer.didDeleteItems(withIdentifiers: delta.deleted.map { NSFileProviderItemIdentifier($0) })
                }
                observer.finishEnumeratingChanges(upTo: NSFileProviderSyncAnchor(SyncAnchorCodec.encode(delta.cursor)),
                                                  moreComing: false)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        let cursor = (try? core.index.cursor()) ?? 0
        completionHandler(NSFileProviderSyncAnchor(SyncAnchorCodec.encode(cursor)))
    }
}
