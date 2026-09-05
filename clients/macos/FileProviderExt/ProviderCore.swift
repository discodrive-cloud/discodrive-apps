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

    func download(nodeID: String) async throws -> URL {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try await mapErrors { try await client.download(nodeID: nodeID, to: tmp) }
        return tmp
    }

    // Translate transport failures into the errors Finder knows how to present.
    private func mapErrors<T>(_ body: () async throws -> T) async throws -> T {
        do { return try await body() }
        catch let e as URLError {
            throw NSFileProviderError(.serverUnreachable, userInfo: [NSUnderlyingErrorKey: e])
        } catch APIError.notAuthenticated {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 401 || code == 403 {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 404 {
            throw NSFileProviderError(.noSuchItem)
        }
    }
}
