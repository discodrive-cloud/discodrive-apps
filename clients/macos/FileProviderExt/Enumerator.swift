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

    private struct PageToken: Codable {
        let version: Int
        let container: String
        let afterID: String
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
                let afterID: String?
                if page.rawValue == NSFileProviderPage.initialPageSortedByName as Data ||
                    page.rawValue == NSFileProviderPage.initialPageSortedByDate as Data {
                    afterID = nil
                } else {
                    guard let token = try? JSONDecoder().decode(PageToken.self, from: page.rawValue),
                          token.version == 1, token.container == container.rawValue else {
                        throw NSFileProviderError(.pageExpired)
                    }
                    afterID = token.afterID
                }
                let limit = 200
                let nodes = try core.index.enumerationPage(
                    parentID: container == .rootContainer ? nil : container.rawValue,
                    workingSet: container == .workingSet, afterID: afterID, limit: limit + 1)
                let items = nodes.prefix(limit)
                let next: NSFileProviderPage?
                if nodes.count > limit, let last = items.last {
                    next = NSFileProviderPage(try JSONEncoder().encode(
                        PageToken(version: 1, container: container.rawValue, afterID: last.id)))
                } else {
                    next = nil
                }
                observer.didEnumerate(items.map(core.item(for:)))
                observer.finishEnumerating(upTo: next)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func enumerateChanges(for observer: NSFileProviderChangeObserver, from anchor: NSFileProviderSyncAnchor) {
        nonisolated(unsafe) let observer = observer
        Task {
            do {
                let since = try ProviderSyncAnchor.decode(anchor.rawValue)
                let limit = min(200, max(1, observer.suggestedBatchSize ?? 200))
                let batch = try await core.pullChangeBatch(since: since, limit: limit)
                let delta = batch.delta
                let updated = delta.updated.compactMap { try? core.index.node(id: $0) }.map(core.item(for:))
                if !updated.isEmpty { observer.didUpdate(updated) }
                if !delta.deleted.isEmpty {
                    observer.didDeleteItems(withIdentifiers: delta.deleted.map { NSFileProviderItemIdentifier($0) })
                }
                observer.finishEnumeratingChanges(upTo: NSFileProviderSyncAnchor(ProviderSyncAnchor.encode(delta.cursor)),
                                                  moreComing: batch.moreComing)
            } catch {
                observer.finishEnumeratingWithError(error)
            }
        }
    }

    func currentSyncAnchor(completionHandler: @escaping (NSFileProviderSyncAnchor?) -> Void) {
        let cursor = (try? core.index.cursor()) ?? 0
        completionHandler(NSFileProviderSyncAnchor(ProviderSyncAnchor.encode(cursor)))
    }
}
