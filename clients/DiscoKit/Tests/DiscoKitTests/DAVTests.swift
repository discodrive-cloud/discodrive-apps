import XCTest
@testable import DiscoKit

final class DAVTests: XCTestCase {
    func testProfileDownloadStaysOnPairedServer() async throws {
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"fixture"}"#.utf8)) }
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fixture")
            XCTAssertEqual(request.url!.path, "/me/apple-profile")
            return (201, [:], Data(#"{"download_path":"/apple-profile/ticket/DiscoDrive.mobileconfig"}"#.utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://server.test:8443")!, deviceToken: "device", session: MockURLProtocol.session())
        let url = try await client.appleProfile(installationID: "test", calendars: true, contacts: false)
        XCTAssertEqual(url.absoluteString, "https://server.test:8443/apple-profile/ticket/DiscoDrive.mobileconfig")
    }
    func testRejectsExternalProfileURL() async throws {
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"fixture"}"#.utf8)) }
            return (201, [:], Data(#"{"download_path":"https://other.test/profile"}"#.utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        do { _ = try await client.appleProfile(installationID: "test", calendars: true, contacts: true); XCTFail("External URL accepted") }
        catch APIError.badResponse {} catch { XCTFail("Unexpected error: \(error)") }
    }
    func testEncryptedEnrollmentKeepsCredentialsOutOfURL() async throws {
        let ticket = String(repeating: "a", count: 64)
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"fixture"}"#.utf8)) }
            XCTAssertEqual(request.url!.path, "/me/apple-enrollment")
            XCTAssertNil(request.url!.query)
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer fixture")
            return (201, [:], Data("{\"download_path\":\"/apple-enrollment/\(ticket)/DiscoDrive.mobileconfig\"}".utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://server.test:8443")!, deviceToken: "device", session: MockURLProtocol.session())
        let url = try await client.appleEnrollment(installationID: "test", calendars: true, contacts: true, credential: .init(id: "id", password: "private-password"))
        XCTAssertEqual(url.absoluteString, "https://server.test:8443/apple-enrollment/\(ticket)/DiscoDrive.mobileconfig")
        XCTAssertFalse(url.absoluteString.contains("private-password"))
    }

    func testEncryptedEnrollmentRejectsUnsafeDownloadPaths() async throws {
        for path in ["https://other.test/profile", "/apple-enrollment/../profile", "/apple-enrollment/short/DiscoDrive.mobileconfig", "/apple-enrollment/" + String(repeating: "a", count: 64) + "/DiscoDrive.mobileconfig?secret=x"] {
            let response = try JSONSerialization.data(withJSONObject: ["download_path": path])
            MockURLProtocol.handler = { request in
                if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"fixture"}"#.utf8)) }
                return (201, [:], response)
            }
            let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
            do { _ = try await client.appleEnrollment(installationID: "test", calendars: true, contacts: true, credential: .init(id: "id", password: "secret")); XCTFail("Unsafe URL accepted") }
            catch APIError.badResponse {} catch { XCTFail("Unexpected error: \(error)") }
        }
    }

    func testEncryptedEnrollmentRequiresHTTPS() async throws {
        MockURLProtocol.handler = { _ in XCTFail("Credential request on HTTP"); return (500, [:], Data()) }
        let client = APIClient(baseURL: URL(string: "http://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        do { _ = try await client.appleEnrollment(installationID: "test", calendars: true, contacts: true, credential: .init(id: "id", password: "secret")); XCTFail("HTTP accepted") }
        catch APIError.badResponse {} catch { XCTFail("Unexpected error: \(error)") }
    }

    func testEncryptedEnrollmentUnavailable() async throws {
        MockURLProtocol.handler = { request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"fixture"}"#.utf8)) }
            XCTAssertEqual(request.url!.path, "/me/apple-enrollment")
            return (200, [:], Data(#"{"enabled":false}"#.utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        let available = try await client.appleEnrollmentAvailable()
        XCTAssertFalse(available)
    }

}
