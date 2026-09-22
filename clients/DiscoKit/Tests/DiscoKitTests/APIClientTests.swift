import XCTest
@testable import DiscoKit

final class APIClientTests: XCTestCase {
    func testTokenExchangeDistinguishesServerFailureFromInvalidCredentials() async throws {
        for status in [401, 403, 429, 500, 503] {
            MockURLProtocol.handler = { _ in (status, [:], Data()) }
            let client = APIClient(baseURL: URL(string: "https://x.test")!,
                                   deviceToken: "D", session: MockURLProtocol.session())
            do {
                _ = try await client.authToken()
                XCTFail("Expected HTTP failure")
            } catch APIError.notAuthenticated {
                XCTAssertTrue([401, 403].contains(status), "HTTP \(status) is not an authentication failure")
            } catch APIError.http(let code) {
                XCTAssertEqual(code, status)
                XCTAssertFalse([401, 403].contains(status))
            }
        }
    }

    func testAllChangesPaginates() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" {
                return (200, [:], Data(#"{"token":"J"}"#.utf8))
            }
            let since = URLComponents(url: req.url!, resolvingAgainstBaseURL: false)!
                .queryItems?.first(where: { $0.name == "since" })?.value ?? "0"
            if since == "0" {
                return (200, [:], Data(#"{"changes":[{"seq":1,"op":"put","node_id":"a","path":"/a","is_dir":false,"version":1,"content_hash":"","size":1,"deleted":false}],"cursor":1,"has_more":true}"#.utf8))
            }
            return (200, [:], Data(#"{"changes":[{"seq":2,"op":"put","node_id":"b","path":"/b","is_dir":false,"version":1,"content_hash":"","size":1,"deleted":false}],"cursor":2,"has_more":false}"#.utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        var collected: [RemoteChange] = []
        let final = try await client.allChanges(since: 0) { page in collected.append(contentsOf: page.changes) }
        XCTAssertEqual(collected.map(\.nodeID), ["a", "b"])
        XCTAssertEqual(final, 2)
    }

    func testDownloadToFile() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            if req.url!.path == "/files/n1/content" { return (200, [:], Data("hello".utf8)) }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        let dst = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try await client.download(nodeID: "n1", to: dst)
        XCTAssertEqual(try String(contentsOf: dst, encoding: .utf8), "hello")
    }

    func testTokenExchangeAndRetryOn401() async throws {
        var callCount = 0
        MockURLProtocol.handler = { req in
            let path = req.url!.path
            if path == "/auth/device/token" {
                return (200, ["Content-Type": "application/json"], Data(#"{"token":"JWT123"}"#.utf8))
            }
            if path == "/sync/changes" {
                callCount += 1
                if callCount == 1 { return (401, [:], Data()) }
                return (200, ["Content-Type": "application/json"],
                        Data(#"{"changes":[],"cursor":0,"has_more":false}"#.utf8))
            }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "DEV", session: MockURLProtocol.session())
        let page = try await client.changes(since: 0, limit: 500)
        XCTAssertEqual(page.cursor, 0)
        XCTAssertEqual(callCount, 2)
    }

    func testGetLanguage() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            if req.url!.path == "/me/language" { return (200, [:], Data(#"{"language":"ru"}"#.utf8)) }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        let lang = try await client.getLanguage()
        XCTAssertEqual(lang, "ru")
    }

    func testSetLanguageSendsPut() async throws {
        var method = ""
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            if req.url!.path == "/me/language" {
                method = req.httpMethod ?? ""
                return (200, [:], Data(#"{"language":"fr"}"#.utf8))
            }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        try await client.setLanguage("fr")
        XCTAssertEqual(method, "PUT")
    }

    func testWriteOps() async throws {
        var seen: [String] = []   // "METHOD path"
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            seen.append("\(req.httpMethod ?? "") \(req.url!.path)")
            switch (req.httpMethod ?? "", req.url!.path) {
            case ("PUT", "/sync/file"):          return (201, [:], Data())
            case ("POST", "/sync/dir"):          return (201, [:], Data())
            case ("DELETE", "/files/n1"):        return (204, [:], Data())
            case ("PATCH", "/files/n1/rename"):  return (200, [:], Data())
            default:                             return (404, [:], Data())
            }
        }
        let c = APIClient(baseURL: URL(string: "https://x.test")!,
                          deviceToken: "D", session: MockURLProtocol.session())
        try await c.uploadFile(relPath: "/Папка/a.txt", data: Data("x".utf8))
        try await c.createDir(relPath: "/Папка")
        try await c.delete(nodeID: "n1")
        try await c.rename(nodeID: "n1", newName: "b.txt")
        XCTAssertEqual(seen, ["PUT /sync/file", "POST /sync/dir", "DELETE /files/n1", "PATCH /files/n1/rename"])
    }
}
