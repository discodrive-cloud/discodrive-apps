import XCTest
@testable import DiscoKit

/// A pairing's verification_uri comes from the server; handed to the system unchecked, a
/// hostile or compromised server could make the app open smb://, ssh:// or a settings pane.
final class URLPolicyTests: XCTestCase {
    private let server = URL(string: "https://Files.Example.com:8443/base")!

    func testTheServersOwnWebPagesAreOpenable() {
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "https://files.example.com:8443/app/pair?code=AB12")!, relativeTo: server))
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "HTTPS://FILES.EXAMPLE.COM:8443/pair")!, relativeTo: server),
                      "scheme and host compare without case")
        // A relative verification_uri resolved against the server is the server.
        let relative = URL(string: "/app/pair?code=1", relativeTo: server)!.absoluteURL
        XCTAssertTrue(URLPolicy.isOpenable(relative, relativeTo: server))
    }

    /// The port is part of the origin; an unwritten one is the scheme's default.
    func testPortsMustMatch() {
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com/pair")!, relativeTo: server),
                       "443 is not the server's 8443")
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com:9000/pair")!, relativeTo: server))
        let plain = URL(string: "https://files.example.com")!
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "https://files.example.com:443/pair")!, relativeTo: plain))
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "https://files.example.com/pair")!, relativeTo: plain))
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com:8080/pair")!, relativeTo: plain))
    }

    /// The scheme is part of the origin: an https server's link is never followed over
    /// plain http, and an http server's link to an https page is shown as text too.
    func testSchemeMustMatch() {
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "http://files.example.com:8443/pair")!, relativeTo: server))
        let http = URL(string: "http://files.example.com:8080")!
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "http://files.example.com:8080/pair")!, relativeTo: http))
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com:8080/pair")!, relativeTo: http))
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com/pair")!,
                                            relativeTo: URL(string: "http://files.example.com")!))
        XCTAssertTrue(URLPolicy.isOpenable(URL(string: "http://files.example.com/pair")!,
                                           relativeTo: URL(string: "http://files.example.com")!))
    }

    func testOtherSchemesAreRefused() {
        for s in ["smb://files.example.com/share", "ssh://files.example.com", "vnc://files.example.com",
                  "file:///etc/passwd", "x-apple.systempreferences:com.apple.preference.security",
                  "javascript:alert(1)", "tel:+100", "ftp://files.example.com/x"] {
            XCTAssertFalse(URLPolicy.isOpenable(URL(string: s)!, relativeTo: server), s)
        }
    }

    func testOtherHostsAreRefused() {
        for s in ["https://attacker.example/pair", "https://files.example.com.attacker.example/pair",
                  "https://files.example.com@attacker.example/pair", "https://example.com/pair", "https:///pair"] {
            guard let url = URL(string: s) else { continue }
            XCTAssertFalse(URLPolicy.isOpenable(url, relativeTo: server), s)
        }
    }

    func testAServerWithoutAHostAllowsNothing() {
        XCTAssertFalse(URLPolicy.isOpenable(URL(string: "https://files.example.com/pair")!,
                                            relativeTo: URL(string: "files.example.com")!))
    }
}

/// The server address itself: https anywhere, plain http only to this machine — the same
/// rule as the Go core's CheckServerURL. ATS is off in the apps, so this is what keeps a
/// device token from ever crossing a network in clear text.
final class ServerURLPolicyTests: XCTestCase {
    // A string Foundation cannot even parse is refused like any other bad address.
    private func allowed(_ s: String) -> Bool { URL(string: s).map(URLPolicy.isAllowedServer) ?? false }

    func testHTTPSIsAllowedAnywhere() {
        for s in ["https://files.example.com", "HTTPS://Files.Example.com:8443/base", "https://10.0.0.2", "https://[fd00::1]:8443"] {
            XCTAssertTrue(allowed(s), s)
        }
    }

    func testPlainHTTPOnlyToLoopback() {
        for s in ["http://localhost:8080", "http://LOCALHOST", "http://127.0.0.1:8080/", "http://[::1]:8080"] {
            XCTAssertTrue(allowed(s), s)
        }
        for s in ["http://files.example.com", "http://10.0.0.2:8080", "http://127.0.0.2", "http://localhost.example.com",
                  "http://192.168.1.5", "http://[fd00::1]"] {
            XCTAssertFalse(allowed(s), s)
        }
    }

    func testOtherShapesAreRefused() {
        for s in ["ftp://files.example.com", "files.example.com", "https://", "https://user:pw@files.example.com",
                  "mailto:a@b.c", "file:///tmp"] {
            XCTAssertFalse(allowed(s), s)
        }
    }

    func testPairingRefusesBeforeAnyRequest() async {
        MockURLProtocol.failure = nil
        var requests = 0
        MockURLProtocol.handler = { _ in requests += 1; return (201, [:], Data()) }
        defer { MockURLProtocol.handler = nil }
        let pairing = Pairing(baseURL: URL(string: "http://files.example.com")!, session: MockURLProtocol.session())
        do {
            _ = try await pairing.start(deviceName: "x")
            XCTFail("expected http:// to be refused")
        } catch {
            XCTAssertTrue(error is InsecureServerURLError, "\(error)")
        }
        do {
            _ = try await pairing.poll(deviceCode: "DC", interval: .milliseconds(1))
            XCTFail("expected http:// to be refused")
        } catch {
            XCTAssertTrue(error is InsecureServerURLError, "\(error)")
        }
        XCTAssertEqual(requests, 0)
    }

    func testRefusalTextAndLocalizedMessage() {
        XCTAssertEqual(InsecureServerURLError().localizedDescription, "server URL must use https")
        for lang in L10n.supported {
            XCTAssertFalse(L10n.table["pairing.httpsRequired"]?[lang]?.isEmpty ?? true, lang)
        }
    }
}
