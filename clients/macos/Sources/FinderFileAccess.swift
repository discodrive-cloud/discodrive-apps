import Foundation
import FileProvider
import DiscoKit
import Darwin
import UniformTypeIdentifiers

/// Browsing in the app and in Finder must open the same system-managed file.
@MainActor
final class FinderFileAccess: LocalFileAccess {
    private var leases: [String: URL] = [:]

    private func manager() throws -> NSFileProviderManager {
        guard let manager = NSFileProviderManager(for: FileProviderDomain.domain) else {
            throw NSFileProviderError(.providerNotFound)
        }
        return manager
    }

    private func location(_ node: Node) async throws -> URL {
        try await manager().getUserVisibleURL(for: NSFileProviderItemIdentifier(node.id))
    }

    func status(of node: Node) async throws -> LocalStatus {
        let url = try await location(node)
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        var info = stat()
        guard lstat(url.path, &info) == 0 else { return .none }
        // stat reads metadata only; inspecting a row must never download its content.
        return info.st_flags & UInt32(SF_DATALESS) == 0 ? .cached : .none
    }

    func download(_ node: Node) async throws -> URL {
        let url = try await location(node)
        let scoped = url.startAccessingSecurityScopedResource()
        do {
            let reader = CoordinatedFileRead(url: url)
            try await withTaskCancellationHandler {
                try await Task.detached { try reader.read() }.value
                try Task.checkCancellation()
            } onCancel: { reader.cancel() }
            // Keep access while preview/export/open callers use the returned URL.
            if scoped {
                leases.removeValue(forKey: node.id)?.stopAccessingSecurityScopedResource()
                leases[node.id] = url
            }
            return url
        } catch {
            if scoped { url.stopAccessingSecurityScopedResource() }
            throw error
        }
    }

    func evict(_ node: Node) async throws {
        try await manager().evictItem(identifier: NSFileProviderItemIdentifier(node.id))
        leases.removeValue(forKey: node.id)?.stopAccessingSecurityScopedResource()
    }

    func rootURL() async throws -> URL {
        let url = try await manager().getUserVisibleURL(for: .rootContainer)
        if url.startAccessingSecurityScopedResource() {
            leases.removeValue(forKey: "_root")?.stopAccessingSecurityScopedResource()
            leases["_root"] = url
        }
        return url
    }

    func cachedIDs() async throws -> [String] {
        let enumerator = try manager().enumeratorForMaterializedItems()
        return try await withCheckedThrowingContinuation { continuation in
            let observer = CachedItemsObserver(enumerator: enumerator) { result in
                enumerator.invalidate()
                continuation.resume(with: result)
            }
            enumerator.enumerateItems(for: observer, startingAt: NSFileProviderPage(Data()))
        }
    }

    func close() {
        for url in leases.values { url.stopAccessingSecurityScopedResource() }
        leases.removeAll()
    }
}

private final class CoordinatedFileRead: @unchecked Sendable {
    private let url: URL
    private let coordinator = NSFileCoordinator()
    init(url: URL) { self.url = url }
    func cancel() { coordinator.cancel() }
    func read() throws {
        var coordinationError: NSError?
        var readError: Error?
        coordinator.coordinate(readingItemAt: url, options: [], error: &coordinationError) { location in
            do {
                let file = try FileHandle(forReadingFrom: location)
                defer { try? file.close() }
                // Also force hydration on systems which initially coordinate metadata.
                _ = try file.read(upToCount: 1)
            } catch { readError = error }
        }
        if let coordinationError { throw coordinationError }
        if let readError { throw readError }
    }
}

private final class CachedItemsObserver: NSObject, NSFileProviderEnumerationObserver {
    private let enumerator: NSFileProviderEnumerator
    private let finish: (Result<[String], Error>) -> Void
    private var identifiers: [String] = []
    init(enumerator: NSFileProviderEnumerator, finish: @escaping (Result<[String], Error>) -> Void) {
        self.enumerator = enumerator
        self.finish = finish
    }
    func didEnumerate(_ items: [NSFileProviderItem]) {
        identifiers += items.filter { $0.isDownloaded == true && $0.contentType?.conforms(to: .directory) == false }
            .map { $0.itemIdentifier.rawValue }
    }
    func finishEnumerating(upTo nextPage: NSFileProviderPage?) {
        if let nextPage { enumerator.enumerateItems(for: self, startingAt: nextPage) }
        else { finish(.success(identifiers)) }
    }
    func finishEnumeratingWithError(_ error: Error) { finish(.failure(error)) }
}
