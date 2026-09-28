import XCTest
@testable import DiscoKit

final class RevokeDeviceTests: XCTestCase {
    private func jwt(_ claims: String) -> String {
        func b64(_ s: String) -> String {
            Data(s.utf8).base64EncodedString().replacingOccurrences(of: "+", with: "-")
                .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        }
        return b64(#"{"alg":"HS256"}"#) + "." + b64(claims) + ".sig"
    }

    /// Signing out ends the device on the server: a token only forgotten locally kept
    /// working for anyone holding a copy.
    func testRevokeDeletesThisDevice() async throws {
        let token = jwt(#"{"sub":"u","did":"5ae54550-a0eb-4081-ac36-0d7761ba2fe3"}"#)
        var deleted: String?
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data("{\"token\":\"\(token)\"}".utf8)) }
            XCTAssertEqual(request.httpMethod, "DELETE")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer \(token)")
            deleted = request.url!.path
            return (204, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        try await client.revokeThisDevice()
        XCTAssertEqual(deleted, "/devices/5ae54550-a0eb-4081-ac36-0d7761ba2fe3")
    }

    /// A device the server already rejects is revoked; nothing else to do.
    func testAlreadyRevokedIsNotAnError() async throws {
        MockURLProtocol.handler = { _ in (401, [:], Data()) }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        try await client.revokeThisDevice()
    }

    /// A session without a device id never leads to deleting anything.
    func testNoDeviceIDDeletesNothing() async throws {
        let token = jwt(#"{"sub":"u"}"#)
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data("{\"token\":\"\(token)\"}".utf8)) }
            XCTFail("unexpected \(request.httpMethod!) \(request.url!.path)")
            return (500, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        do { try await client.revokeThisDevice(); XCTFail("expected an error") } catch APIError.badResponse {}
    }
}
