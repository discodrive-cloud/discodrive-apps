import XCTest
@testable import DiscoKit

final class PairingTests: XCTestCase {
    override func tearDown() {
        MockURLProtocol.failure = nil
        MockURLProtocol.handler = nil
        super.tearDown()
    }

    // Approving the code means leaving for a browser, and an app that is no longer in the
    // foreground loses its connections. Those failures must not end the pairing.
    func testPollSurvivesDroppedConnections() async throws {
        var attempts = 0
        MockURLProtocol.failure = { _ in
            attempts += 1
            return attempts <= 2 ? URLError(.networkConnectionLost) : nil
        }
        MockURLProtocol.handler = { _ in
            (200, [:], Data(#"{"status":"approved","device_token":"DEVTOK"}"#.utf8))
        }
        let p = Pairing(baseURL: URL(string: "https://x.test")!, session: MockURLProtocol.session())
        let token = try await p.poll(deviceCode: "DC", interval: .milliseconds(1))
        XCTAssertEqual(token, "DEVTOK")
    }

    // The retry above must not hang forever on a server that is genuinely unreachable.
    func testPollGivesUpOnSustainedNetworkFailure() async throws {
        MockURLProtocol.failure = { _ in URLError(.cannotConnectToHost) }
        let p = Pairing(baseURL: URL(string: "https://x.test")!, session: MockURLProtocol.session())
        do {
            _ = try await p.poll(deviceCode: "DC", interval: .milliseconds(1),
                                 networkGrace: .milliseconds(30))
            XCTFail("expected an error once the grace window elapsed")
        } catch is URLError {
            // expected
        }
    }

    func testPairInitThenPollApproved() async throws {
        var polls = 0
        MockURLProtocol.handler = { req in
            switch req.url!.path {
            case "/pair/init":
                return (201, [:], Data(#"{"device_code":"DC","user_code":"AB-CD","verification_uri":"https://x.test/pair","interval":1,"expires_in":300}"#.utf8))
            case "/pair/token":
                polls += 1
                if polls == 1 { return (200, [:], Data(#"{"status":"pending"}"#.utf8)) }
                return (200, [:], Data(#"{"status":"approved","device_token":"DEVTOK"}"#.utf8))
            default: return (404, [:], Data())
            }
        }
        let p = Pairing(baseURL: URL(string: "https://x.test")!, session: MockURLProtocol.session())
        let info = try await p.start(deviceName: "Mac")
        XCTAssertEqual(info.userCode, "AB-CD")
        let token = try await p.poll(deviceCode: info.deviceCode, interval: .milliseconds(1))
        XCTAssertEqual(token, "DEVTOK")
    }
}
