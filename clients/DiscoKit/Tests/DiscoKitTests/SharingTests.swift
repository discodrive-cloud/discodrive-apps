import XCTest
@testable import DiscoKit

final class SharingTests: XCTestCase {
    func testCreateListAndRevokeShare() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer J")
            switch (req.httpMethod!, req.url!.path) {
            case ("POST", "/files/n/share"):
                XCTAssertEqual(req.value(forHTTPHeaderField: "Content-Type"), "application/json")
                let body: Data
                if let data = req.httpBody { body = data }
                else if let stream = req.httpBodyStream {
                    stream.open(); defer { stream.close() }
                    var bytes = [UInt8](repeating: 0, count: 2048), data = Data()
                    while stream.hasBytesAvailable { let n = stream.read(&bytes, maxLength: bytes.count); if n <= 0 { break }; data.append(contentsOf: bytes.prefix(n)) }
                    body = data
                } else { body = Data() }
                let payload = try! JSONSerialization.jsonObject(with: body) as! [String: Any]
                XCTAssertEqual(payload["access"] as? String, "read")
                XCTAssertEqual(payload["link"] as? Bool, true)
                XCTAssertEqual(payload["expires_in_seconds"] as? Int, 86400)
                XCTAssertNil(payload["email"])
                return (201, [:], Data(#"{"share_id":"s","token":"secret"}"#.utf8))
            case ("GET", "/files/n/shares"):
                return (200, [:], Data(#"[{"share_id":"s","kind":"link","access":"read"}]"#.utf8))
            case ("DELETE", "/shares/s"): return (204, [:], Data())
            default: XCTFail("Unexpected request"); return (404, [:], Data())
            }
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        let result = try await client.share(nodeID: "n", email: nil, expiresInSeconds: 86400)
        let url = await client.shareURL(token: try XCTUnwrap(result.token))
        XCTAssertEqual(url.absoluteString, "https://server.test/s/secret")
        let shares = try await client.shares(nodeID: "n")
        XCTAssertEqual(shares.map(\.id), ["s"])
        try await client.revokeShare(id: result.share_id)
    }
}
