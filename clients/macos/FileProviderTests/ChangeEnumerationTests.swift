import XCTest
import FileProvider
import DiscoKit

private final class ChangeObserver: NSObject, NSFileProviderChangeObserver, @unchecked Sendable {
    var updated: [String] = []
    var deleted: [String] = []
    var anchor: NSFileProviderSyncAnchor?
    var moreComing = false
    var error: Error?
    let done: XCTestExpectation
    let suggestedBatchSize: Int
    init(_ done: XCTestExpectation, size: Int = 200) { self.done = done; suggestedBatchSize = size }
    func didUpdate(_ items: [NSFileProviderItem]) { updated += items.map { $0.itemIdentifier.rawValue } }
    func didDeleteItems(withIdentifiers identifiers: [NSFileProviderItemIdentifier]) { deleted += identifiers.map(\.rawValue) }
    func finishEnumeratingChanges(upTo anchor: NSFileProviderSyncAnchor, moreComing: Bool) {
        self.anchor = anchor; self.moreComing = moreComing; done.fulfill()
    }
    func finishEnumeratingWithError(_ error: Error) { self.error = error; done.fulfill() }
}

final class ChangeEnumerationTests: XCTestCase {
    private func core() throws -> ProviderCore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        addTeardownBlock {
            MockURLProtocol.handler = nil
            MockURLProtocol.failure = nil
            try? FileManager.default.removeItem(at: directory)
        }
        return try ProviderCore(index: IndexStore(path: directory.appendingPathComponent("index.sqlite").path),
            client: APIClient(baseURL: URL(string: "https://example.invalid")!, deviceToken: "test", session: MockURLProtocol.session()))
    }

    func testLargeChangeFeedYieldsBoundedBatchesAndResumesFromReturnedAnchor() async throws {
        try await verifyBatches(size: 1000, indexAhead: false)
    }

    func testSuggestedSizeAndSharedIndexAheadDoNotSkipChanges() async throws {
        try await verifyBatches(size: 37, indexAhead: true)
    }

    private func verifyBatches(size: Int, indexAhead: Bool) async throws {
        let core = try core()
        if indexAhead {
            try core.index.apply((1...1201).map { i in
                RemoteChange(seq: Int64(i), op: "put", nodeID: "n\(i)", path: "file-\(i).txt",
                             isDir: false, version: 1, contentHash: "", size: 1, deleted: false)
            })
            try core.index.setCursor(9000)
        }
        MockURLProtocol.handler = { request in
            if request.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"test"}"#.utf8))
            }
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!
            let since = Int(query.first { $0.name == "since" }!.value!)!
            let limit = Int(query.first { $0.name == "limit" }!.value!)!
            let end = min(since + limit, 1201)
            let changes: [[String: Any]] = since < end ? ((since + 1)...end).map { i in
                ["seq": i, "op": "put", "node_id": "n\(i)", "path": "file-\(i).txt",
                 "is_dir": false, "version": 1, "content_hash": "", "size": 1, "deleted": false]
            } : []
            return (200, [:], try! JSONSerialization.data(withJSONObject:
                ["changes": changes, "cursor": end, "has_more": end < 1201]))
        }
        let enumerator = Enumerator(core: core, container: .workingSet)
        var anchor = NSFileProviderSyncAnchor(ProviderSyncAnchor.encode(0))
        var seen = Set<String>()
        for _ in 0..<40 {
            let done = expectation(description: "bounded change batch")
            let observer = ChangeObserver(done, size: size)
            enumerator.enumerateChanges(for: observer, from: anchor)
            await fulfillment(of: [done], timeout: 15)
            XCTAssertNil(observer.error)
            XCTAssertLessThanOrEqual(observer.updated.count, min(200, size), "A change callback must not buffer the full feed in the memory-limited extension")
            XCTAssertTrue(seen.isDisjoint(with: observer.updated))
            seen.formUnion(observer.updated)
            anchor = try XCTUnwrap(observer.anchor)
            XCTAssertEqual(try ProviderSyncAnchor.decode(anchor.rawValue), Int64(seen.count))
            XCTAssertEqual(observer.moreComing, seen.count < 1201)
            if !observer.moreComing { break }
        }
        XCTAssertEqual(seen.count, 1201)
        XCTAssertEqual(try core.index.cursor(), indexAhead ? 9000 : 1201)
    }

    func testFailedPageKeepsAnchorAndDeleteRestoreSurvivesBatchBoundaries() async throws {
        let core = try core()
        var rejectSecondPage = true
        MockURLProtocol.handler = { request in
            if request.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"test"}"#.utf8))
            }
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!
            let since = Int(query.first { $0.name == "since" }!.value!)!
            if since == 1 && rejectSecondPage { return (503, [:], Data()) }
            let seq = since + 1
            let change: [String: Any] = ["seq": seq, "op": seq == 2 ? "delete" : "put",
                "node_id": "restored", "path": "file.txt", "is_dir": false, "version": seq,
                "content_hash": "", "size": 1, "deleted": seq == 2]
            return (200, [:], try! JSONSerialization.data(withJSONObject:
                ["changes": [change], "cursor": seq, "has_more": seq < 3]))
        }
        let enumerator = Enumerator(core: core, container: .workingSet)
        var anchor = NSFileProviderSyncAnchor(ProviderSyncAnchor.encode(0))
        var present = false
        for attempt in 0..<4 {
            let done = expectation(description: "change or transient failure")
            let observer = ChangeObserver(done, size: 1)
            enumerator.enumerateChanges(for: observer, from: anchor)
            await fulfillment(of: [done], timeout: 15)
            if attempt == 1 {
                XCTAssertEqual((observer.error as? NSFileProviderError)?.code, .serverUnreachable)
                XCTAssertNil(observer.anchor)
                XCTAssertEqual(try core.index.cursor(), 1)
                rejectSecondPage = false
                continue
            }
            XCTAssertNil(observer.error)
            if observer.updated.contains("restored") { present = true }
            if observer.deleted.contains("restored") { present = false }
            anchor = try XCTUnwrap(observer.anchor)
            XCTAssertEqual(observer.moreComing, attempt < 3)
        }
        XCTAssertTrue(present)
        XCTAssertNotNil(try core.index.node(id: "restored"))
        XCTAssertEqual(try ProviderSyncAnchor.decode(anchor.rawValue), 3)
    }
}
