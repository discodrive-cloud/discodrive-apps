import XCTest
import DiscoKit

@MainActor
final class LocalFileAccessTests: XCTestCase {
    private final class Files: LocalFileAccess {
        let url: URL
        var requested = false
        var downloaded = false
        var release: CheckedContinuation<Void, Never>?
        init(url: URL) { self.url = url }
        func status(of node: Node) async throws -> LocalStatus { downloaded ? .cached : .none }
        func download(_ node: Node) async throws -> URL {
            requested = true
            await withCheckedContinuation { release = $0 }
            downloaded = true
            return url
        }
        func evict(_ node: Node) async throws { downloaded = false }
        func close() { release?.resume(); release = nil }
    }

    func testBrowserUsesProviderFileAndShowsLoadingUntilItIsReady() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: root); MockURLProtocol.handler = nil }
        MockURLProtocol.handler = { @Sendable _ in (500, [:], Data()) }
        let app = AppState(storageDirectory: root, automaticallyConnect: false, networkSession: MockURLProtocol.session())
        app.activate(serverURL: URL(string: "https://test.invalid")!, token: "test", pin: nil)
        try app.index?.apply([RemoteChange(seq: 1, op: "put", nodeID: "file", path: "/file.mp3", isDir: false,
                                          version: 1, contentHash: "", size: 123, deleted: false)])
        let node = try XCTUnwrap(app.index?.node(id: "file"))
        let files = Files(url: root.appendingPathComponent("Finder/file.mp3"))
        app.fileAccess = files
        let operation = Task { await app.ensureDownloaded(node) }
        for _ in 0..<100 {
            if files.requested { break }
            try await Task.sleep(for: .milliseconds(5))
        }
        XCTAssertTrue(files.requested, "download must use the same file provider as Finder")
        XCTAssertTrue(app.isDownloading(node), "large files need a visible loading state")
        files.close()
        let result = await operation.value
        XCTAssertEqual(result, files.url)
        XCTAssertFalse(app.isDownloading(node))
        await app.refreshLocalStatus(node)
        XCTAssertEqual(app.status(of: node), .cached)
        files.downloaded = false
        await app.refreshLocalStatus(node)
        XCTAssertEqual(app.status(of: node), .none, "eviction in Finder must clear the app's checkmark")
        files.downloaded = true
        await app.refreshLocalStatus(node)
        XCTAssertEqual(app.status(of: node), .cached, "a download in Finder must update the app without downloading again")
    }
}
