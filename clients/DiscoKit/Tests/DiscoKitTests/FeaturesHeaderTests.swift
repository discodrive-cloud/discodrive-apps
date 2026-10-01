import XCTest
@testable import DiscoKit

// The server only reports change-feed deletes of permanently purged nodes to a client
// that declares it applies deletes by node id (and ignores deletes for unknown ids).
// Every request this client makes must carry that declaration — not just the changes
// request — so a released client is never mistaken for one that deletes by path.
final class FeaturesHeaderTests: XCTestCase {
    func testFeaturesHeaderSentOnChangesRequest() async throws {
        var sawHeaderOnChanges: String?
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            if req.url!.path == "/sync/changes" {
                sawHeaderOnChanges = req.value(forHTTPHeaderField: APIClient.featuresHeaderField)
                return (200, [:], Data(#"{"changes":[],"cursor":0,"has_more":false}"#.utf8))
            }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        _ = try await client.changes(since: 0, limit: 500)
        XCTAssertEqual(sawHeaderOnChanges, APIClient.featuresHeaderValue)
    }

    func testFeaturesHeaderSentOnNormalRequest() async throws {
        var sawHeaderOnLanguage: String?
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            if req.url!.path == "/me/language" {
                sawHeaderOnLanguage = req.value(forHTTPHeaderField: APIClient.featuresHeaderField)
                return (200, [:], Data(#"{"language":"en"}"#.utf8))
            }
            return (404, [:], Data())
        }
        let client = APIClient(baseURL: URL(string: "https://x.test")!,
                               deviceToken: "D", session: MockURLProtocol.session())
        _ = try await client.getLanguage()
        XCTAssertEqual(sawHeaderOnLanguage, APIClient.featuresHeaderValue)
    }
}
