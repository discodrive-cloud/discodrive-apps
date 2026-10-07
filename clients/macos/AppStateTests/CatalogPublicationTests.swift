import XCTest
import DiscoKit
import os

@MainActor
final class CatalogPublicationTests: XCTestCase {
    func testFirstPageIsNotPublishedBeforeLastPageArrives() async throws {
        try await checkInitialDownload(failLastPage: false)
    }

    func testFailedInitialDownloadStaysHiddenUntilRetryCompletes() async throws {
        try await checkInitialDownload(failLastPage: true)
    }

    func testChildArrivingBeforeParentNeverFlashesAtRoot() async throws {
        try await checkInitialDownload(failLastPage: false, nestedFolder: true)
    }

    private func checkInitialDownload(failLastPage: Bool, nestedFolder: Bool = false) async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: root) }
        let gate = DispatchSemaphore(value: 0)
        let waiting = OSAllocatedUnfairLock(initialState: false)
        defer { gate.signal(); MockURLProtocol.handler = nil }
        let firstNames = ["books", "docs", "music", "obsidian"]
        let remaining = ["Camera Uploads", "DeviceUploads", "Downloads", "photos", "pics", "sync", "tracks", "vaults"]
        func page(_ names: [String], start: Int, more: Bool, removeObsidian: Bool = false) throws -> Data {
            var changes: [[String: Any]] = names.enumerated().map { offset, name in
                ["seq": start + offset, "op": "put", "node_id": name, "path": nestedFolder && name == "obsidian" ? "/sync/obsidian" : "/" + name,
                 "is_dir": true, "version": 1, "content_hash": "", "size": 0, "deleted": false]
            }
            if removeObsidian {
                changes.append(["seq": start + names.count, "op": "del", "node_id": "obsidian", "path": "/obsidian",
                                "is_dir": true, "version": 2, "content_hash": "", "size": 0, "deleted": true])
            }
            return try JSONSerialization.data(withJSONObject: ["changes": changes, "cursor": start + changes.count - 1, "has_more": more])
        }
        let first = try page(firstNames, start: 1, more: true)
        let last = try page(remaining, start: 5, more: false, removeObsidian: !nestedFolder)
        MockURLProtocol.handler = { @Sendable request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"test"}"#.utf8)) }
            let since = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "since" }?.value
            if since == "0" { return (200, [:], first) }
            waiting.withLock { $0 = true }
            _ = gate.wait(timeout: .now() + 10)
            return (failLastPage ? 500 : 200, [:], last)
        }
        let app = AppState(storageDirectory: root, automaticallyConnect: false, networkSession: MockURLProtocol.session())
        app.activate(serverURL: URL(string: "https://test.invalid")!, token: "new-pairing", pin: nil)
        let refresh = Task { await app.refresh() }
        for _ in 0..<500 {
            if waiting.withLock({ $0 }) { break }
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(waiting.withLock { $0 }, "the real refresh must be paused on page two")
        XCTAssertEqual(try app.index?.children(of: nil).count, 4, "first page has reached SQLite")
        XCTAssertTrue(app.tree.isEmpty)
        XCTAssertTrue(app.children(of: nil).isEmpty, "do not display the four partial folders from the screenshot")
        XCTAssertTrue(app.fileListLoading)
        // Restarting while the initial index is partial must not make it visible.
        let restarted = AppState(storageDirectory: root, automaticallyConnect: false)
        restarted.activate(serverURL: URL(string: "https://test.invalid")!, token: "new-pairing", pin: nil)
        await restarted.restoreCatalog()
        XCTAssertTrue(restarted.children(of: nil).isEmpty)
        XCTAssertTrue(restarted.tree.isEmpty)
        gate.signal()
        let success = await refresh.value
        if failLastPage {
            XCTAssertFalse(success)
            XCTAssertTrue(app.children(of: nil).isEmpty)
            XCTAssertTrue(app.tree.isEmpty)
            XCTAssertNotNil(app.fileListError)
            XCTAssertFalse(try XCTUnwrap(app.index).catalogIsComplete())
            MockURLProtocol.handler = { @Sendable _ in (200, [:], last) }
            let recovered = await app.refresh()
            XCTAssertTrue(recovered)
        } else {
            XCTAssertTrue(success)
        }
        XCTAssertEqual(app.children(of: nil).count, 11)
        XCTAssertEqual(app.tree.count, 11)
        XCTAssertFalse(app.children(of: nil).contains { $0.name == "obsidian" })
        if nestedFolder {
            XCTAssertEqual(app.children(of: "sync").map(\.name), ["obsidian"])
        }
        await restarted.restoreCatalog()
        XCTAssertEqual(restarted.children(of: nil).count, 11, "completed catalog remains available offline")
    }
}
