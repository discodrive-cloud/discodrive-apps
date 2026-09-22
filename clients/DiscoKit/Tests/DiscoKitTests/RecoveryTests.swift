import XCTest
@testable import DiscoKit

final class RecoveryTests: XCTestCase {
    func testTrashAndVersionContracts() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer J")
            switch (req.httpMethod!, req.url!.path) {
            case ("GET", "/files/trash"):
                return (200, [:], Data(#"[{"id":"folder","name":"Documents","is_dir":true,"size":null,"deleted_at":null}]"#.utf8))
            case ("GET", "/files/file/versions"):
                return (200, [:], Data(#"[{"version":42,"size":null,"is_conflict_loser":true}]"#.utf8))
            case ("POST", "/files/file/restore"):
                let stream = req.httpBodyStream
                stream?.open(); defer { stream?.close() }
                var bytes = [UInt8](repeating: 0, count: 512)
                let count = stream?.read(&bytes, maxLength: bytes.count) ?? 0
                let data = req.httpBody ?? Data(bytes.prefix(max(0, count)))
                let body = try! JSONDecoder().decode([String: Int64].self, from: data)
                XCTAssertEqual(body, ["version": 42])
                return (200, [:], Data("{}".utf8))
            case ("POST", "/files/folder/undelete"): return (200, [:], Data("{}".utf8))
            case ("DELETE", "/files/folder/purge"), ("DELETE", "/files/trash"): return (204, [:], Data())
            default: XCTFail("Unexpected recovery request"); return (404, [:], Data())
            }
        }
        let api = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        let trash = try await api.trash()
        XCTAssertEqual(trash.first?.id, "folder"); XCTAssertNil(trash.first?.size)
        let versions = try await api.versions(nodeID: "file")
        XCTAssertEqual(versions.first?.version, 42); XCTAssertEqual(versions.first?.is_conflict_loser, true)
        try await api.undelete(id: "folder")
        try await api.restoreVersion(nodeID: "file", version: 42)
        try await api.purge(id: "folder")
        try await api.emptyTrash()
    }

    func testRecoveryFailureIsNotReportedAsSuccess() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            return (409, [:], Data())
        }
        let api = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        do { try await api.undelete(id: "conflict"); XCTFail("Expected conflict") }
        catch APIError.http(let status) { XCTAssertEqual(status, 409) }
    }
}
