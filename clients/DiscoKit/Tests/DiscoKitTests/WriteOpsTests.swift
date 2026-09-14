import XCTest
import GRDB
@testable import DiscoKit

// What the File Provider extension needs to write: move, base-version-guarded uploads
// that say whether the server kept a newer version, and knowing what lives inside a vault.
final class WriteOpsTests: XCTestCase {
    final class Seen: @unchecked Sendable {
        var requests: [String] = []
        var headers: [String: String] = [:]
        var body: [String: Any] = [:]
        var chunks = 0
    }

    private func client(_ seen: Seen, conflicted: Bool) -> APIClient {
        MockURLProtocol.handler = { req in
            let path = req.url!.path
            func json(_ obj: [String: Any], _ code: Int = 200) -> (Int, [String: String], Data) {
                (code, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: obj))
            }
            if path.hasSuffix("/auth/device/token") { return json(["token": "jwt"]) }
            seen.requests.append("\(req.httpMethod!) \(path)")
            if path.hasSuffix("/move") {
                seen.body = (try? JSONSerialization.jsonObject(with: MockURLProtocol.lastBody ?? Data())) as? [String: Any] ?? [:]
                return json(["node": ["id": "n1"]])
            }
            if path.hasSuffix("/sync/file") {
                seen.headers = req.allHTTPHeaderFields ?? [:]
                return json(["node": ["id": "n1", "version": 5], "conflicted": conflicted], 201)
            }
            if path.hasSuffix("/upload/init") {
                seen.body = (try? JSONSerialization.jsonObject(with: MockURLProtocol.lastBody ?? Data())) as? [String: Any] ?? [:]
                return json(["upload_id": "u1", "next_chunk": 0], 201)
            }
            if path.contains("/chunk/") { seen.chunks += 1; return json(["next_chunk": seen.chunks]) }
            if path.hasSuffix("/complete") { return json(["node": ["id": "n1", "version": 6], "conflicted": conflicted], 201) }
            return json([:], 404)
        }
        return APIClient(baseURL: URL(string: "https://x.test")!, deviceToken: "D", session: MockURLProtocol.session())
    }

    private func fixture(_ bytes: Int) throws -> URL {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("ddk-\(UUID().uuidString).bin")
        try Data(repeating: 7, count: bytes).write(to: url)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return url
    }

    func testMoveSendsTheNewParent() async throws {
        let seen = Seen()
        let api = client(seen, conflicted: false)
        try await api.move(nodeID: "n1", newParentID: "d2")
        XCTAssertEqual(seen.requests, ["PATCH /files/n1/move"])
        XCTAssertEqual(seen.body["parent_id"] as? String, "d2")
        try await api.move(nodeID: "n1", newParentID: nil)
        XCTAssertTrue(seen.body["parent_id"] is NSNull, "the root is spelled null: \(seen.body)")
    }

    func testSmallUploadCarriesTheBaseVersionAndReportsAConflict() async throws {
        let seen = Seen()
        let api = client(seen, conflicted: true)
        let out = try await api.upload(fileURL: try fixture(100), relPath: "/a.txt", modifiedAt: nil, baseVersion: 4, chunkSize: 4096)
        XCTAssertEqual(seen.headers["X-Base-Version"], "4")
        XCTAssertTrue(out.conflicted)
        XCTAssertEqual(out.nodeID, "n1")
        XCTAssertEqual(out.version, 5)
    }

    func testChunkedUploadCarriesTheBaseVersionAndReportsTheOutcome() async throws {
        let seen = Seen()
        let api = client(seen, conflicted: false)
        let out = try await api.upload(fileURL: try fixture(5000), relPath: "/b.bin", modifiedAt: nil, baseVersion: 9, chunkSize: 4096)
        XCTAssertEqual(seen.body["base_version"] as? Int, 9)
        XCTAssertFalse(out.conflicted)
        XCTAssertEqual(out.version, 6)
    }

    func testIndexKnowsWhatLivesInsideAVault() throws {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        func put(_ id: String, _ path: String, dir: Bool = false) -> RemoteChange {
            RemoteChange(seq: 1, op: "put", nodeID: id, path: path, isDir: dir, version: 1, contentHash: "", size: 0, deleted: false)
        }
        try store.apply([
            // Paths as the server sends them: no leading slash.
            put("v", "Safe", dir: true), put("vk", "Safe/vault.cryptomator"), put("d", "Safe/d", dir: true),
            put("f", "Safe/d/AB/x.c9r"), put("plain", "Docs/a.txt"), put("docs", "Docs", dir: true),
        ])
        XCTAssertTrue(try store.isInsideVault(path: "Safe/d/AB/x.c9r"))
        XCTAssertTrue(try store.isInsideVault(path: "Safe/vault.cryptomator"), "the vault's own files are part of it")
        XCTAssertTrue(try store.isInsideVault(path: "Safe"), "the vault folder itself is not for Finder to write into")
        XCTAssertFalse(try store.isInsideVault(path: "Docs/a.txt"))
        XCTAssertFalse(try store.isInsideVault(path: "Docs"))
        XCTAssertEqual(IndexStore.path(in: "", name: "a"), "a")
        XCTAssertEqual(IndexStore.path(in: "Docs", name: "a"), "Docs/a")
    }
}
