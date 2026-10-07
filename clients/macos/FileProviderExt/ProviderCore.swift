import os
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
// stored pairing. Only missing credentials are a sign-in error; local failures stay distinct.
final class ProviderCore: @unchecked Sendable {
    static let log = Logger(subsystem: "org.discodrive.ext", category: "provider")   // IndexStore and APIClient are thread-safe
    let index: IndexStore
    let client: APIClient

    init() throws {
        KeychainConfig.accessGroup = ProviderConfig.keychainGroup
        guard let path = ProviderConfig.indexPath else {
            Self.log.error("Shared container unavailable")
            throw NSFileProviderError(.cannotSynchronize)
        }
        let token: String?, urlStr: String?, pin: String?
        do {
            token = try KeychainToken.loadShared(service: KeychainToken.tokenService)
            urlStr = try KeychainToken.loadShared(service: KeychainToken.serverService)
            pin = try KeychainToken.loadShared(service: KeychainToken.pinService)
        } catch {
            Self.log.error("Shared credentials unavailable: OSStatus \((error as NSError).code)")
            throw NSFileProviderError(.cannotSynchronize, userInfo: [NSUnderlyingErrorKey: error])
        }
        guard let token, let urlStr else { throw NSFileProviderError(.notAuthenticated) }
        // https, or http to this machine only: the extension has ATS off, like the app.
        guard let url = URL(string: urlStr), URLPolicy.isAllowedServer(url) else {
            Self.log.error("Invalid stored server URL")
            throw NSFileProviderError(.cannotSynchronize)
        }
        let index: IndexStore
        do { index = try IndexStore(path: path) }
        catch {
            Self.log.error("Shared index unavailable: \(String(describing: error), privacy: .public)")
            throw NSFileProviderError(.cannotSynchronize, userInfo: [NSUnderlyingErrorKey: error])
        }
        self.index = index
        // The certificate the user trusted at pairing; set before the client's first request.
        DiscoNet.pin = pin
        self.client = APIClient(baseURL: url, deviceToken: token)
    }

    init(index: IndexStore, client: APIClient) {
        self.index = index
        self.client = client
    }

    typealias Delta = ChangeDelta

    // A File Provider change callback must yield before loading the next server page.
    // Replaying the entire feed at once can exceed the iOS extension's memory limit;
    // the system then retries the same anchor and eventually pauses synchronization.
    func pullChangeBatch(since: Int64, limit: Int) async throws -> (delta: Delta, moreComing: Bool) {
        let page = try await mapErrors { try await client.changes(since: since, limit: limit) }
        try index.apply(page.changes)
        try index.setCursor(page.cursor)
        var delta = Delta(cursor: page.cursor)
        delta.record(page.changes)
        // The shared index can be ahead (the app also updates it). Only acknowledge
        // the page actually reported to the system, never that shared index cursor.
        return (delta, page.hasMore)
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
            delta.record(page.changes)
            cursor = page.cursor
            if !page.hasMore { break }
        }
        try index.setCursor(cursor)
        // The cursor of the pages read here, not the index's: the app applies pages to the
        // same index, and an anchor past what this delta reports would have the system
        // never ask for the changes in between.
        delta.cursor = cursor
        return delta
    }

    /// The item for a node, read-only when it belongs to a vault.
    func item(for node: Node) -> ProviderItem { item(for: node, unverifiedBytes: nil) }

    func item(for node: Node, unverifiedBytes: String?) -> ProviderItem {
        let inVault = (try? index.isInsideVault(path: node.path)) ?? false
        let isRoot = node.isDir && (try? index.node(atPath: node.path + "/vault.cryptomator")) != nil
        return ProviderItem(node: node, writable: !inVault, vaultRoot: isRoot, unverifiedBytes: unverifiedBytes)
    }

    /// Downloads a file and says which version the bytes are. The download carries no
    /// version of its own, so the bytes are hashed and matched: against the node the index
    /// held, else — the server had moved on — against what a pull brings. Bytes that still
    /// match nothing (the server moved on again mid-download) are fetched anew; if that
    /// never settles they are served without a version, and an edit of them is then
    /// guarded as an edit of unknown base.
    func fetch(_ node: Node) async throws -> (URL, ProviderItem) {
        var known = node
        var last: (url: URL, hash: String)?
        for _ in 0..<3 {
            if let last { try? FileManager.default.removeItem(at: last.url) }
            let url = try await download(nodeID: known.id)
            let hash = try ContentHash.sha256Hex(of: url)
            if let owner = ContentHash.owner(ofDownloaded: hash, before: known, after: nil) { return (url, item(for: owner)) }
            _ = try await pull(since: try index.cursor())
            guard let now = try index.node(id: known.id) else {
                try? FileManager.default.removeItem(at: url)
                throw NSFileProviderError(.noSuchItem)
            }
            if let owner = ContentHash.owner(ofDownloaded: hash, before: known, after: now) { return (url, item(for: owner)) }
            Self.log.error("downloaded bytes of \(known.id, privacy: .public) match neither v\(known.version) nor v\(now.version); fetching again")
            last = (url, hash)
            known = now
        }
        guard let last else { throw NSFileProviderError(.serverUnreachable) }
        return (last.url, item(for: known, unverifiedBytes: last.hash))
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
    // A node the server answers "not found" about was deleted there while its delete event
    // never arrived: the index forgets it and its subtree, and Finder is told .noSuchItem,
    // which drops the item on its side too.
    func rename(nodeID: String, to name: String) async throws {
        try await mapErrors { try await index.forgettingIfGone(nodeID) { try await client.rename(nodeID: nodeID, newName: name) } }
    }
    func move(nodeID: String, toParent parent: String?) async throws {
        try await mapErrors {
            do { try await client.move(nodeID: nodeID, newParentID: parent) }
            catch APIError.nodeNotFound {
                let client = self.client
                let gone = await index.forgetGoneAfterMove(nodeID: nodeID, parentID: parent) { try await client.nodeExists(nodeID: $0) }
                Self.log.notice("move of \(nodeID, privacy: .public): not found on the server; forgot \(gone.count) node(s)")
                throw APIError.nodeNotFound
            }
        }
    }
    // A node the server no longer has is already deleted: that is the outcome asked for.
    func delete(nodeID: String) async throws {
        let gone = try await mapErrors { try await index.deleteForgettingGone(nodeID: nodeID) { try await client.delete(nodeID: nodeID) } }
        if !gone.isEmpty { Self.log.notice("delete of \(nodeID, privacy: .public): already gone on the server; forgot \(gone.count) node(s)") }
    }

    func download(nodeID: String) async throws -> URL {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try await mapErrors { try await index.forgettingIfGone(nodeID) { try await client.download(nodeID: nodeID, to: tmp) } }
        return tmp
    }

    // Translate transport failures into the errors Finder knows how to present.
    func mapErrors<T>(_ body: () async throws -> T) async throws -> T {
        do { return try await body() }
        catch let e as URLError {
            if let changed = DiscoNet.explain(e) as? CertificateChangedError {
                NSLog("DiscoDrive FP: %@", changed.localizedDescription)
            }
            NSLog("DiscoDrive FP: transport failure %ld %@", e.code.rawValue, e.localizedDescription)
            throw NSFileProviderError(.serverUnreachable, userInfo: [NSUnderlyingErrorKey: e])
        } catch APIError.notAuthenticated {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 401 || code == 403 {
            throw NSFileProviderError(.notAuthenticated)
        } catch APIError.http(let code) where code == 429 || code >= 500 {
            throw NSFileProviderError(.serverUnreachable)
        } catch APIError.http(let code) where code == 404 {
            throw NSFileProviderError(.noSuchItem)
        } catch APIError.nodeNotFound {
            throw NSFileProviderError(.noSuchItem)
        } catch {
            NSLog("DiscoDrive FP: %@", String(describing: error))
            throw error
        }
    }
}
