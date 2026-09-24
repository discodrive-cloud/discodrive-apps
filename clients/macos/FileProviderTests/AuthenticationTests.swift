import XCTest
import FileProvider
import DiscoKit

final class AuthenticationTests: XCTestCase {
    private final class Factory: @unchecked Sendable {
        var attempts = 0 // Called under the extension's lock.
        let core: ProviderCore
        init(core: ProviderCore) { self.core = core }
        func make() throws -> ProviderCore {
            attempts += 1
            if attempts == 1 { throw NSFileProviderError(.cannotSynchronize) }
            return core
        }
    }

    private func core() throws -> ProviderCore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return try ProviderCore(index: IndexStore(path: directory.appendingPathComponent("index.sqlite").path),
                                client: APIClient(baseURL: URL(string: "https://example.invalid")!, deviceToken: "test"))
    }

    func testInitializationFailureIsRetriedWithoutRestartingExtension() throws {
        let factory = Factory(core: try core())
        let provider = FileProviderExtension(domain: NSFileProviderDomain(identifier: .init("DiscoDrive"), displayName: ""),
                                             makeCore: { try factory.make() })
        XCTAssertThrowsError(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest())) {
            XCTAssertEqual(($0 as NSError).code, NSFileProviderError.cannotSynchronize.rawValue)
        }
        XCTAssertNoThrow(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest()))
        XCTAssertNoThrow(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest()))
        XCTAssertEqual(factory.attempts, 2)
    }

    func testServerFailureDoesNotAskFinderToSignIn() async throws {
        let core = try core()
        for status in [429, 500, 503] {
            do {
                try await core.mapErrors { throw APIError.http(status) }
                XCTFail("Expected error")
            } catch let error as NSFileProviderError {
                XCTAssertEqual(error.code, .serverUnreachable)
            }
        }
    }
}

private final class PageObserver: NSObject, NSFileProviderEnumerationObserver, @unchecked Sendable {
    var ids: [String] = []
    var next: NSFileProviderPage?
    var error: Error?
    let done: XCTestExpectation
    init(done: XCTestExpectation) { self.done = done }
    func didEnumerate(_ items: [NSFileProviderItem]) { ids += items.map { $0.itemIdentifier.rawValue } }
    func finishEnumerating(upTo nextPage: NSFileProviderPage?) { next = nextPage; done.fulfill() }
    func finishEnumeratingWithError(_ error: Error) { self.error = error; done.fulfill() }
}

extension AuthenticationTests {
    func testWorkingSetIsBoundedAcrossRealEnumerationRequests() async throws {
        let core = try core()
        try core.index.apply((1...1201).map { i in
            RemoteChange(seq: Int64(i), op: "put", nodeID: String(format: "n%05d", i), path: "file-\(i).txt", isDir: false, version: 1, contentHash: "", size: 1, deleted: false)
        })
        let enumerator = Enumerator(core: core, container: .workingSet)
        var page = NSFileProviderPage(NSFileProviderPage.initialPageSortedByName as Data)
        var seen = Set<String>()
        for _ in 0..<10 {
            let done = expectation(description: "page")
            let observer = PageObserver(done: done)
            enumerator.enumerateItems(for: observer, startingAt: page)
            await fulfillment(of: [done], timeout: 30)
            XCTAssertNil(observer.error)
            XCTAssertLessThanOrEqual(observer.ids.count, 200, "One request must not buffer the full working set")
            XCTAssertTrue(seen.isDisjoint(with: observer.ids))
            seen.formUnion(observer.ids)
            guard let next = observer.next else { break }
            if seen.count == 200 {
                // Removing an already enumerated row must not shift the next page.
                try core.index.apply([RemoteChange(seq: 1202, op: "delete", nodeID: "n00001",
                    path: "file-1.txt", isDir: false, version: 2, contentHash: "", size: 0, deleted: true)])
            }
            page = next
        }
        XCTAssertEqual(seen.count, 1201)
    }
}

extension AuthenticationTests {
    func testEnumerationPagesStayWithinTheirFolder() async throws {
        let core = try core()
        try core.index.apply([
            RemoteChange(seq: 1, op: "put", nodeID: "folder", path: "folder", isDir: true,
                         version: 1, contentHash: "", size: 0, deleted: false),
            RemoteChange(seq: 2, op: "put", nodeID: "child", path: "folder/child", isDir: false,
                         version: 1, contentHash: "", size: 1, deleted: false),
            RemoteChange(seq: 3, op: "put", nodeID: "root-file", path: "root-file", isDir: false,
                         version: 1, contentHash: "", size: 1, deleted: false)
        ])
        for (container, expected) in [(NSFileProviderItemIdentifier.rootContainer, ["folder", "root-file"]),
                                       (NSFileProviderItemIdentifier("folder"), ["child"])] {
            let done = expectation(description: "folder page")
            let observer = PageObserver(done: done)
            Enumerator(core: core, container: container).enumerateItems(for: observer,
                startingAt: NSFileProviderPage(NSFileProviderPage.initialPageSortedByName as Data))
            await fulfillment(of: [done], timeout: 10)
            XCTAssertNil(observer.error)
            XCTAssertNil(observer.next)
            XCTAssertEqual(observer.ids, expected)
        }
    }

    func testInvalidEnumerationPageExpiresInsteadOfRestarting() async throws {
        let core = try core()
        try core.index.setCursor(1)
        let done = expectation(description: "invalid page")
        let observer = PageObserver(done: done)
        Enumerator(core: core, container: .workingSet).enumerateItems(for: observer,
            startingAt: NSFileProviderPage(Data("invalid".utf8)))
        await fulfillment(of: [done], timeout: 10)
        XCTAssertTrue(observer.ids.isEmpty)
        XCTAssertEqual((observer.error as? NSFileProviderError)?.code, .pageExpired)
    }
}
