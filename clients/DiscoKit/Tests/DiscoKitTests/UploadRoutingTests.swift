import XCTest
@testable import DiscoKit

// `APIClient.upload(fileURL:...)` is the one entry point the apps use for a file off disk:
// small files go up in one PUT, anything longer than a chunk takes the resumable protocol.
final class UploadRoutingTests: XCTestCase {
    final class Calls: @unchecked Sendable {
        var puts = 0
        var inits = 0
        var chunks = 0
        var completes = 0
        var assembled = Data()
        var initBody: [String: Any] = [:]
    }

    private func fixture(_ bytes: Int) throws -> (URL, Data) {
        var payload = Data(count: bytes)
        for i in 0..<bytes { payload[i] = UInt8(97 + i % 26) }
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("ddk-\(UUID().uuidString).bin")
        try payload.write(to: url)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return (url, payload)
    }

    private func client(_ calls: Calls) -> APIClient {
        MockURLProtocol.handler = { req in
            let path = req.url!.path
            func json(_ obj: [String: Any], _ code: Int = 200) -> (Int, [String: String], Data) {
                (code, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: obj))
            }
            if path.hasSuffix("/auth/device/token") { return json(["token": "jwt"]) }
            if path.hasSuffix("/sync/file") { calls.puts += 1; return json([:], 201) }
            if path.hasSuffix("/upload/init") {
                calls.inits += 1
                calls.initBody = (try? JSONSerialization.jsonObject(with: MockURLProtocol.lastBody ?? Data())) as? [String: Any] ?? [:]
                return json(["upload_id": "u1", "next_chunk": 0], 201)
            }
            if path.contains("/chunk/") {
                calls.chunks += 1
                calls.assembled.append(MockURLProtocol.lastBody ?? Data())
                return json(["next_chunk": calls.chunks])
            }
            if path.hasSuffix("/complete") { calls.completes += 1; return json(["node": ["id": "n1"]], 201) }
            return json([:], 404)
        }
        return APIClient(baseURL: URL(string: "https://x.test")!, deviceToken: "D", session: MockURLProtocol.session())
    }

    func testLargeFileTakesTheChunkedProtocol() async throws {
        let (url, payload) = try fixture(10_000)
        let calls = Calls()
        let api = client(calls)
        try await api.upload(fileURL: url, relPath: "/docs/big.bin", parentID: "d1",
                             modifiedAt: Date(timeIntervalSince1970: 1_560_602_400), chunkSize: 4096)
        XCTAssertEqual(calls.puts, 0, "a file longer than a chunk must not go up in one PUT")
        XCTAssertEqual(calls.inits, 1)
        XCTAssertEqual(calls.chunks, 3)
        XCTAssertEqual(calls.completes, 1)
        XCTAssertEqual(calls.assembled, payload)
        XCTAssertEqual(calls.initBody["parent_id"] as? String, "d1")
        XCTAssertEqual(calls.initBody["name"] as? String, "big.bin")
        XCTAssertEqual(calls.initBody["size"] as? Int, 10_000)
        XCTAssertNotNil(calls.initBody["modified_at"], "the content's own date must travel with the upload")
    }

    func testSmallFileGoesUpInOnePut() async throws {
        let (url, _) = try fixture(1000)
        let calls = Calls()
        let api = client(calls)
        try await api.upload(fileURL: url, relPath: "/docs/small.bin", parentID: "d1", modifiedAt: nil, chunkSize: 4096)
        XCTAssertEqual(calls.puts, 1)
        XCTAssertEqual(calls.inits, 0, "a file that fits in one chunk has nothing to resume")
    }

    func testFileExactlyOneChunkLongGoesUpInOnePut() async throws {
        let (url, _) = try fixture(4096)
        let calls = Calls()
        let api = client(calls)
        try await api.upload(fileURL: url, relPath: "/a.bin", parentID: nil, modifiedAt: nil, chunkSize: 4096)
        XCTAssertEqual(calls.puts, 1)
        XCTAssertEqual(calls.inits, 0)
    }
}
