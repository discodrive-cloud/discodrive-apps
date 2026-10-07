import XCTest
import DiscoKit

@MainActor
final class AccountCatalogTests: XCTestCase {
    func testSurvivingPreviousAccountIndexIsNotShownBeforeFirstRefresh() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        let server = URL(string: "https://test.invalid")!
        let previous = AppState(storageDirectory: root, automaticallyConnect: false)
        previous.activate(serverURL: server, token: "previous-account", pin: nil)
        let oldIndex = try XCTUnwrap(previous.index)
        try oldIndex.apply([RemoteChange(seq: 1, op: "put", nodeID: "old-folder", path: "/Old account", isDir: true,
                                        version: 1, contentHash: "", size: 0, deleted: false)])
        await previous.rebuildTree()
        XCTAssertEqual(previous.tree.map(\.id), ["old-folder"])

        // A surviving cache (failed cleanup or another process retaining it) must not
        // be trusted merely because the next pairing uses the same local directory.
        let next = AppState(storageDirectory: root, automaticallyConnect: false)
        next.activate(serverURL: server, token: "next-account", pin: nil)
        await next.rebuildTree()
        XCTAssertFalse(next.fileListLoaded)
        XCTAssertTrue(next.children(of: nil).isEmpty, "the new account must not show old rows while loading")
        XCTAssertTrue(next.tree.isEmpty, "the sidebar must not show the previous account")
        // Even a late writer still holding the previous account's database cannot
        // repopulate the new account's catalog.
        try oldIndex.apply([RemoteChange(seq: 2, op: "put", nodeID: "late-folder", path: "/Late old folder", isDir: true,
                                        version: 1, contentHash: "", size: 0, deleted: false)])
        await next.rebuildTree()
        XCTAssertTrue(next.children(of: nil).isEmpty)
        XCTAssertTrue(next.tree.isEmpty)
        // An ordinary restart of the same pairing retains its own offline catalog.
        let reopened = AppState(storageDirectory: root, automaticallyConnect: false)
        reopened.activate(serverURL: server, token: "previous-account", pin: nil)
        await reopened.rebuildTree()
        XCTAssertEqual(Set(reopened.tree.map(\.id)), ["old-folder", "late-folder"])
    }
    func testRePairClearsVisibleCatalogAndPreservesDisconnectedDownloads() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        let app = AppState(storageDirectory: root, automaticallyConnect: false)
        app.activate(serverURL: URL(string: "https://test.invalid")!, token: "old", pin: nil)
        try app.index?.apply([RemoteChange(seq: 1, op: "put", nodeID: "old", path: "/Old", isDir: true,
                                          version: 1, contentHash: "", size: 0, deleted: false)])
        await app.rebuildTree()
        let local = root.appendingPathComponent("local")
        let content = local.appendingPathComponent("content")
        try FileManager.default.createDirectory(at: content, withIntermediateDirectories: true)
        try Data("saved file".utf8).write(to: content.appendingPathComponent("keep.txt"))
        let oldDatabase = try XCTUnwrap(app.client).transferIdentity
        try app.resetLocalState()
        XCTAssertTrue(app.tree.isEmpty)
        XCTAssertTrue(app.children(of: nil).isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: root.appendingPathComponent("index-\(oldDatabase).sqlite").path))
        let backup = try XCTUnwrap(FileManager.default.contentsOfDirectory(at: local, includingPropertiesForKeys: nil)
            .first { $0.lastPathComponent.hasPrefix("content.old-") })
        XCTAssertEqual(try Data(contentsOf: backup.appendingPathComponent("keep.txt")), Data("saved file".utf8))
        // The reset also runs when confirming the next pairing; it must not delete the backup.
        try app.resetLocalState()
        app.activate(serverURL: URL(string: "https://other.invalid")!, token: "old", pin: nil)
        XCTAssertTrue(app.children(of: nil).isEmpty)
        XCTAssertTrue(FileManager.default.fileExists(atPath: backup.appendingPathComponent("keep.txt").path))
    }

    func testCleanupFailureIsNotSilentlyAccepted() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock {
            try? FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: root.path)
            try? FileManager.default.removeItem(at: root)
        }
        let app = AppState(storageDirectory: root, automaticallyConnect: false)
        app.activate(serverURL: URL(string: "https://test.invalid")!, token: "old", pin: nil)
        try FileManager.default.setAttributes([.posixPermissions: 0o500], ofItemAtPath: root.path)
        XCTAssertThrowsError(try app.resetLocalState())
        XCTAssertTrue(app.tree.isEmpty)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: root.path)
        XCTAssertNoThrow(try app.resetLocalState(), "cleanup can be retried before accepting another pairing")
    }

    func testChangingServerClearsPublishedTreeBeforeAnyNetworkResponse() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        let app = AppState(storageDirectory: root, automaticallyConnect: false)
        app.activate(serverURL: URL(string: "https://first.invalid")!, token: "same-token", pin: nil)
        try app.index?.apply([RemoteChange(seq: 1, op: "put", nodeID: "old", path: "/Old", isDir: true,
                                          version: 1, contentHash: "", size: 0, deleted: false)])
        await app.rebuildTree()
        XCTAssertFalse(app.tree.isEmpty)
        app.activate(serverURL: URL(string: "https://second.invalid")!, token: "same-token", pin: nil)
        XCTAssertTrue(app.fileListLoading)
        XCTAssertTrue(app.tree.isEmpty)
        XCTAssertTrue(app.children(of: nil).isEmpty)
        await app.rebuildTree()
        XCTAssertTrue(app.tree.isEmpty)
    }

}
