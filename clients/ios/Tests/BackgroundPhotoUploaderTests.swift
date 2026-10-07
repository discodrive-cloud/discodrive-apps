import XCTest
import DiscoKit
@testable import DiscoDrive

private final class PhotoProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var handler: (@Sendable (URLRequest) -> (Int, Data))?
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let (status, data) = Self.handler?(request) ?? (500, Data())
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

private final class PhotoServer: @unchecked Sendable {
    private let lock = NSLock()
    var next = 0
    var opens = 0
    var published = false
    var completions = 0
    var failCompleteResponse = false
    var rejectFirstChunk: Int?
    var authorizations = 0
    var chunks: [Int] = []
    func reply(_ request: URLRequest) -> (Int, Data) {
        lock.lock(); defer { lock.unlock() }
        let path = request.url!.path
        func json(_ code: Int, _ value: Any) -> (Int, Data) { (code, try! JSONSerialization.data(withJSONObject: value)) }
        if path == "/auth/device/token" { authorizations += 1; return json(200, ["token":"jwt"]) }
        if path == "/files" {
            return json(200, published ? [["id":"node", "parent_id":"folder", "name":"photo.dng", "is_dir":false, "size":9, "mtime":"2026-10-02T00:00:00Z", "content_hash":"sha", "version":1]] : [])
        }
        if path == "/upload/init" { opens += 1; return json(201, ["upload_id":"session", "next_chunk":next]) }
        if path == "/upload/session" { return json(published ? 404 : 200, ["next_chunk":next]) }
        if path.contains("/chunk/") {
            if let status = rejectFirstChunk { rejectFirstChunk = nil; return json(status, [:]) }
            let index = Int(path.split(separator: "/").last!)!
            chunks.append(index); next = max(next, index + 1)
            return json(200, ["next_chunk":next])
        }
        if path == "/upload/session/complete" {
            completions += 1; published = true
            return json(failCompleteResponse ? 503 : 201, ["node_id":"node", "version":1])
        }
        return json(500, ["error":"unexpected request \(path)"])
    }
}

@MainActor
final class BackgroundPhotoUploaderTests: XCTestCase {
    private func fixture(_ server: PhotoServer, bytes: Int = 9) throws -> (URL, AutoUploadSettings, APIClient, UploadJournal, PhotoTransferStore, BackgroundPhotoUploader, PhotoTransfer) {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let store = try PhotoTransferStore(directory: root)
        let defaults = UserDefaults(suiteName: "photo-tests-" + UUID().uuidString)!
        let settings = AutoUploadSettings(defaults: defaults)
        settings.enabled = true; settings.wifiOnly = false; settings.requireBattery = false
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [PhotoProtocol.self]
        PhotoProtocol.handler = { server.reply($0) }
        let api = APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test", session: URLSession(configuration: config))
        let journal = try UploadJournal(path: root.appendingPathComponent("journal.sqlite").path)
        let uploader = BackgroundPhotoUploader(api: api, store: store, journal: journal, settings: settings, configuration: config)
        var job = PhotoTransfer(account: api.transferIdentity, assetID: "asset", modified: Date(timeIntervalSince1970: 10), created: Date(), parentID: "folder", name: "photo.dng", sha: "sha", bytes: Int64(bytes))
        job.chunkSize = ChunkedUploader.defaultChunkSize
        let export = root.appendingPathComponent("export")
        try Data(repeating: 0x5a, count: bytes).write(to: export)
        try uploader.enqueue(job, exportedFile: export)
        return (root, settings, api, journal, store, uploader, job)
    }

    private func waitUntil(_ condition: () throws -> Bool, seconds: Double = 5) async throws {
        let end = Date().addingTimeInterval(seconds)
        while try !condition(), Date() < end { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(try condition())
    }

    func testTwoChunksPublishOnlyAfterServerCompletion() async throws {
        let server = PhotoServer()
        let (root, settings, _, journal, store, uploader, job) = try fixture(server, bytes: ChunkedUploader.defaultChunkSize + 9)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        XCTAssertFalse(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertTrue(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        XCTAssertEqual(server.chunks, [0,1])
        XCTAssertEqual(server.completions, 1)
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.assetURL(job).path))
        try await uploader.logout()
    }

    func testLostPublicationResponseReconcilesWithoutSecondUpload() async throws {
        let server = PhotoServer(); server.failCompleteResponse = true
        let (root, settings, _, journal, store, uploader, job) = try fixture(server)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).first?.attempts == 1 }
        XCTAssertFalse(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        var retry = try XCTUnwrap(store.all(account: job.account).first)
        retry.retryAt = .distantPast
        try store.save(retry)
        await uploader.resume()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertTrue(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        XCTAssertEqual(server.opens, 1)
        XCTAssertEqual(server.chunks, [0])
        XCTAssertEqual(server.completions, 1)
        try await uploader.logout()
    }

    func testRestoredQueueUsesAcknowledgedServerPosition() async throws {
        let server = PhotoServer(); server.next = 1
        let (root, settings, _, journal, store, uploader, job) = try fixture(server, bytes: ChunkedUploader.defaultChunkSize + 9)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        var checkpoint = job
        checkpoint.uploadID = "session" // Lost chunk response: DB still says zero.
        try store.save(checkpoint)
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertEqual(server.chunks, [1])
        XCTAssertEqual(server.opens, 0)
        XCTAssertTrue(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        try await uploader.logout()
    }

    func testLogoutDiscardsQueueWithoutPublishing() async throws {
        let server = PhotoServer()
        let (root, settings, _, journal, store, uploader, job) = try fixture(server)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        try await uploader.logout()
        await uploader.restore()
        XCTAssertTrue(try store.all(account: job.account).isEmpty)
        XCTAssertFalse(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        XCTAssertEqual(server.opens, 0)
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.assetURL(job).path))
    }
    func testExpiredAuthenticationKeepsExportAndRefreshesToken() async throws {
        let server = PhotoServer(); server.rejectFirstChunk = 401
        let (root, settings, _, journal, store, uploader, job) = try fixture(server)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).first?.attempts == 1 }
        XCTAssertTrue(FileManager.default.fileExists(atPath: store.assetURL(job).path))
        XCTAssertFalse(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        var retry = try XCTUnwrap(store.all(account: job.account).first)
        retry.retryAt = .distantPast; try store.save(retry)
        await uploader.resume()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertEqual(server.authorizations, 2)
        XCTAssertEqual(server.opens, 1)
        XCTAssertEqual(server.chunks, [0])
        try await uploader.logout()
    }

    func testProxyBodyLimitStartsFreshSessionWithSmallerParts() async throws {
        let server = PhotoServer(); server.rejectFirstChunk = 413
        let (root, settings, _, journal, store, uploader, job) = try fixture(server)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertEqual(server.opens, 2)
        XCTAssertEqual(server.completions, 1)
        XCTAssertTrue(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        try await uploader.logout()
    }

    func testMissingExportBecomesDiscoverableWithoutBeingMarkedSent() async throws {
        let server = PhotoServer()
        let (root, settings, _, journal, store, uploader, job) = try fixture(server)
        defer { settings.reset(); try? FileManager.default.removeItem(at: root) }
        try FileManager.default.removeItem(at: store.assetURL(job))
        await uploader.restore()
        try await waitUntil { try store.all(account: job.account).isEmpty }
        XCTAssertFalse(try journal.isKnown(assetID: job.assetID, modified: job.modified))
        XCTAssertEqual(server.opens, 0)
        try await uploader.logout()
    }

}
