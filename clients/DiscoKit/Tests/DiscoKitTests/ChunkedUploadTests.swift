import XCTest
@testable import DiscoKit

/// The chunked upload is what makes a phone upload survivable: a dropped connection
/// continues from the server's next_chunk instead of restarting a video from zero.
final class ChunkedUploadTests: XCTestCase {

    private var session: URLSession!
    private var tmpDir: URL!

    override func setUp() {
        super.setUp()
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [MockURLProtocol.self]
        session = URLSession(configuration: cfg)
        tmpDir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try? FileManager.default.createDirectory(at: tmpDir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        MockURLProtocol.handler = nil
        try? FileManager.default.removeItem(at: tmpDir)
        super.tearDown()
    }

    private func file(bytes: Int, named: String = "clip.mp4", modified: Date? = nil) throws -> URL {
        let url = tmpDir.appendingPathComponent(named)
        var data = Data(count: bytes)
        for i in 0..<bytes { data[i] = UInt8(65 + i % 26) }
        try data.write(to: url)
        if let modified {
            try FileManager.default.setAttributes([.modificationDate: modified], ofItemAtPath: url.path)
        }
        return url
    }

    /// Collects what the server received so a test can assert on the reassembled file.
    private final class FakeServer: @unchecked Sendable {
        var assembled = Data()
        var nextChunk = 0
        var inits: [[String: Any]] = []
        var chunkCalls = 0
        /// (chunkIndex, status) to fail once, or nil.
        var failOnce: (Int, Int)?
        var failed = false
        var completeStatus = 201
    }

    private func install(_ srv: FakeServer) {
        MockURLProtocol.handler = { req in
            let path = req.url!.path
            func json(_ obj: [String: Any], _ code: Int = 200) -> (Int, [String: String], Data) {
                (code, ["Content-Type": "application/json"],
                 try! JSONSerialization.data(withJSONObject: obj))
            }
            if path.hasSuffix("/auth/device/token") { return json(["token": "jwt"]) }
            if path.hasSuffix("/upload/init") {
                let body = MockURLProtocol.lastBody ?? Data()
                let obj = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] ?? [:]
                srv.inits.append(obj)
                srv.assembled = Data()
                srv.nextChunk = 0
                return json(["upload_id": "u\(srv.inits.count)", "next_chunk": 0], 201)
            }
            if path.contains("/upload/"), path.contains("/chunk/") {
                srv.chunkCalls += 1
                let n = Int(path.components(separatedBy: "/").last ?? "0") ?? 0
                if let (failAt, status) = srv.failOnce, !srv.failed, n == failAt {
                    srv.failed = true
                    return json(["error": "injected", "next_chunk": srv.nextChunk], status)
                }
                if n < srv.nextChunk { return json(["next_chunk": srv.nextChunk]) }
                if n > srv.nextChunk {
                    return json(["error": "chunk out of order", "next_chunk": srv.nextChunk], 409)
                }
                srv.assembled.append(MockURLProtocol.lastBody ?? Data())
                srv.nextChunk += 1
                return json(["next_chunk": srv.nextChunk])
            }
            if path.hasSuffix("/complete") {
                if srv.completeStatus != 201 {
                    return json(["error": "upload is incomplete: staged bytes do not match the declared size"],
                                srv.completeStatus)
                }
                return json(["node": ["id": "n1", "version": 1]], 201)
            }
            if path.contains("/upload/") { return json(["next_chunk": srv.nextChunk]) }
            return json([:], 404)
        }
    }

    func testSendsWholeFileWithSizeAndDate() async throws {
        let srv = FakeServer(); install(srv)
        let when = Date(timeIntervalSince1970: 1_562_000_000)
        let url = try file(bytes: 20_000, modified: when)
        let api = APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt", session: session)
        let uploader = ChunkedUploader(api: api, chunkSize: 4_096)

        try await uploader.upload(fileURL: url, parentID: "p1", name: "photo.jpg", modifiedAt: when)

        let expected = try Data(contentsOf: url)
        XCTAssertEqual(srv.assembled, expected, "the reassembled file must match byte for byte")
        XCTAssertEqual(srv.inits.count, 1)
        XCTAssertEqual(srv.inits[0]["name"] as? String, "photo.jpg")
        XCTAssertEqual(srv.inits[0]["parent_id"] as? String, "p1")
        XCTAssertEqual(srv.inits[0]["size"] as? Int, expected.count)
        XCTAssertNotNil(srv.inits[0]["modified_at"], "the content date must travel with the upload")
    }

    /// A 409 means the client and the server disagree about the position; the server wins.
    func testResumesAfterOutOfOrder() async throws {
        let srv = FakeServer(); srv.failOnce = (2, 409); install(srv)
        let url = try file(bytes: 20_000)
        let api = APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt", session: session)
        let uploader = ChunkedUploader(api: api, chunkSize: 4_096)

        try await uploader.upload(fileURL: url, parentID: nil, name: "photo.jpg", modifiedAt: nil)

        XCTAssertEqual(srv.assembled, try Data(contentsOf: url))
        XCTAssertEqual(srv.inits.count, 1, "a 409 resyncs, it does not start a new session")
    }

    /// A 404 means the session is gone (server GC or restart) — start one fresh, once.
    func testReInitsOnLostSession() async throws {
        let srv = FakeServer(); srv.failOnce = (2, 404); install(srv)
        let url = try file(bytes: 20_000)
        let api = APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt", session: session)
        let uploader = ChunkedUploader(api: api, chunkSize: 4_096)

        try await uploader.upload(fileURL: url, parentID: nil, name: "photo.jpg", modifiedAt: nil)

        XCTAssertEqual(srv.inits.count, 2, "the lost session must be re-inited")
        XCTAssertEqual(srv.assembled, try Data(contentsOf: url))
    }

    /// The file changed while it was being sent. The caller has to tell that apart from a
    /// transport failure: retrying the same session can only fail the same way.
    func testSurfacesSizeMismatch() async throws {
        let srv = FakeServer(); srv.completeStatus = 400; install(srv)
        let url = try file(bytes: 8_000)
        let api = APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt", session: session)
        let uploader = ChunkedUploader(api: api, chunkSize: 4_096)

        do {
            try await uploader.upload(fileURL: url, parentID: nil, name: "photo.jpg", modifiedAt: nil)
            XCTFail("expected the size mismatch to surface")
        } catch UploadError.fileChangedDuringUpload {
            // expected
        }
    }

    func testSingleChunkFileStillCompletes() async throws {
        let srv = FakeServer(); install(srv)
        let url = try file(bytes: 100)
        let api = APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt", session: session)
        let uploader = ChunkedUploader(api: api, chunkSize: 4_096)

        try await uploader.upload(fileURL: url, parentID: nil, name: "tiny.jpg", modifiedAt: nil)
        XCTAssertEqual(srv.assembled, try Data(contentsOf: url))
    }
}

/// Deciding a name before uploading is what keeps auto-upload from overwriting: the server
/// treats a same-named upload as a new version of the existing file.
final class FolderListingTests: XCTestCase {

    private func client(_ handler: @escaping @Sendable (URLRequest) -> (Int, [String: String], Data)) -> APIClient {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [MockURLProtocol.self]
        MockURLProtocol.handler = handler
        return APIClient(baseURL: URL(string: "https://example.test")!, deviceToken: "dt",
                         session: URLSession(configuration: cfg))
    }

    func testListingCarriesHashesAndSizes() async throws {
        let api = client { req in
            func json(_ obj: Any, _ code: Int = 200) -> (Int, [String: String], Data) {
                (code, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: obj))
            }
            if req.url!.path.hasSuffix("/auth/device/token") { return json(["token": "jwt"]) }
            return json([
                ["id": "n1", "name": "IMG_1.jpg", "is_dir": false, "size": 12,
                 "version": 1, "content_hash": "H1", "modified_at": "2019-07-14T10:30:00Z"],
                ["id": "d1", "name": "sub", "is_dir": true, "version": 1, "modified_at": "2019-07-14T10:30:00Z"],
            ])
        }
        let entries = try await api.listFolder(parentID: "p1")
        XCTAssertEqual(entries.count, 2)
        XCTAssertEqual(entries[0].contentHash, "H1")
        XCTAssertEqual(entries[0].size, 12)
        XCTAssertTrue(entries[1].isDir)
    }

    /// An existing folder must not cost a create call — a pass resolves its destination
    /// every time it runs.
    func testEnsureFolderReusesWhatIsThere() async throws {
        let created = Locked(0)
        let api = client { req in
            func json(_ obj: Any, _ code: Int = 200) -> (Int, [String: String], Data) {
                (code, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: obj))
            }
            let path = req.url!.path
            if path.hasSuffix("/auth/device/token") { return json(["token": "jwt"]) }
            if path.hasSuffix("/files/folder") {
                created.value += 1
                return json(["id": "new1", "name": "Pixel", "is_dir": true, "version": 1], 201)
            }
            return json([["id": "d9", "name": "DeviceUploads", "is_dir": true, "version": 1,
                          "modified_at": "2019-07-14T10:30:00Z"]])
        }
        let id = try await api.ensureFolder(parentID: nil, name: "DeviceUploads")
        XCTAssertEqual(id, "d9")
        XCTAssertEqual(created.value, 0, "an existing folder must not be re-created")
    }
}

/// Tiny box so the mock handler can count calls without tripping Swift 6 concurrency.
final class Locked<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: T
    init(_ v: T) { stored = v }
    var value: T {
        get { lock.lock(); defer { lock.unlock() }; return stored }
        set { lock.lock(); stored = newValue; lock.unlock() }
    }
}
