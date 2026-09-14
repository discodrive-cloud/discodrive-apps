import Foundation
import FileProvider
import DiscoKit

// What the extension shares with the app: the index in the App Group container and the
// device token in the keychain group. Both identifiers come from Info.plist, the same keys
// the app reads, so the two processes cannot disagree about where things are.
enum ProviderConfig {
    static let appGroup = Bundle.main.infoDictionary?["DiscoDriveAppGroup"] as? String
    static let keychainGroup = Bundle.main.infoDictionary?["DiscoDriveKeychainGroup"] as? String

    static var indexPath: String? {
        guard let appGroup,
              let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
        else { return nil }
        let dir = container.appendingPathComponent("DiscoDrive", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appendingPathComponent("index.sqlite").path
    }
}

// The extension's view of the server: the shared index plus an API client built from the
// stored pairing. Nil when the app has not paired yet — every request then fails with
// notAuthenticated, which Finder shows as "sign in required".
final class ProviderCore: @unchecked Sendable {   // IndexStore and APIClient are thread-safe
    let index: IndexStore
    let client: APIClient

    init?() {
        KeychainConfig.accessGroup = ProviderConfig.keychainGroup
        guard let path = ProviderConfig.indexPath,
              let token = KeychainToken.load(service: KeychainToken.tokenService),
              let urlStr = KeychainToken.load(service: KeychainToken.serverService),
              let url = URL(string: urlStr),
              let index = try? IndexStore(path: path) else { return nil }
        self.index = index
        self.client = APIClient(baseURL: url, deviceToken: token)
    }

    struct Delta {
        var updated: [String] = []
        var deleted: [String] = []
        var cursor: Int64
    }

    // Pull every change after `since` into the index and say which nodes moved. The app
    // may have applied the same pages already (it listens to the server's event stream);
    // applying them again is idempotent and the cursor never moves backwards.
    func pull(since: Int64) async throws -> Delta {
        var delta = Delta(cursor: since)
        var cursor = since
        while true {
            let page = try await mapErrors { try await client.changes(since: cursor, limit: 500) }
            try index.apply(page.changes)
            for ch in page.changes {
                if ch.deleted { delta.deleted.append(ch.nodeID) } else { delta.updated.append(ch.nodeID) }
            }
            cursor = page.cursor
            if !page.hasMore { break }
        }
        try index.setCursor(cursor)
        delta.cursor = try index.cursor()
        return delta
    }

    /// The item for a node, read-only when it belongs to a vault.
    func item(for node: Node) -> ProviderItem {
        ProviderItem(node: node, writable: !((try? index.isInsideVault(path: node.path)) ?? false))
    }

    /// The server path of the folder an item identifier names ("" for the root).
    func folderPath(_ id: NSFileProviderItemIdentifier) throws -> String {
        if id == .rootContainer { return "" }
        guard let node = try index.node(id: id.rawValue), node.isDir else { throw NSFileProviderError(.noSuchItem) }
        return node.path
    }

    /// Pulls what the server now says and returns the node at `path`, which a write just
    /// created or changed. The pull is what makes the extension's own write visible to
    /// Finder with the server's id and version, the same as a change made elsewhere.
    func pullAndFind(path: String) async throws -> Node {
        _ = try await pull(since: try index.cursor())
        guard let node = try index.node(atPath: path) else { throw NSFileProviderError(.noSuchItem) }
        return node
    }

    // The client operations the write path uses, with transport failures translated.
    func createFolder(path: String) async throws { try await mapErrors { try await client.createDir(relPath: path) } }
    func upload(fileURL: URL, path: String, baseVersion: Int64?) async throws -> APIClient.UploadOutcome {
        try await mapErrors { try await client.upload(fileURL: fileURL, relPath: path,
                                                     modifiedAt: APIClient.contentModificationDate(of: fileURL),
                                                     baseVersion: baseVersion) }
    }
    func rename(nodeID: String, to name: String) async throws { try await mapErrors { try await client.rename(nodeID: nodeID, newName: name) } }
    func move(nodeID: String, toParent parent: String?) async throws { try await mapErrors { try await client.move(nodeID: nodeID, newParentID: parent) } }
    func delete(nodeID: String) async throws { try await mapErrors { try await client.delete(nodeID: nodeID) } }

    func download(nodeID: String) async throws -> URL {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try await mapErrors { try await client.download(nodeID: nodeID, to: tmp) }
        return tmp
    }

    // Translate transport failures into the errors Finder knows how to present.
    func mapErrors<T>(_ body: () async throws -> T) async throws -> T {
        do { return try await body() }
        catch let e as URLError {
            NSLog("DiscoDrive FP: transport failure %ld %@", e.code.rawValue, e.localizedDescription)
            throw NSFileProviderError(.serverUnreachable, userInfo: [NSUnderlyingErrorKey: e])
        } catch APIError.notAuthenticated {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 401 || code == 403 {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 404 {
            throw NSFileProviderError(.noSuchItem)
        } catch {
            NSLog("DiscoDrive FP: %@", String(describing: error))
            throw error
        }
    }
}
