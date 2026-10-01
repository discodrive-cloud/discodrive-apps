import XCTest
import DiscoKit

/// The pinned path inside the app process, where App Transport Security applies (it does not
/// to a macOS test bundle). Needs a self-signed TLS server; skipped otherwise, e.g.:
///
///   openssl s_server -accept 18443 -cert self.pem -key self.key -www -tls1_2 &
///   TEST_RUNNER_DD_PIN_SERVER=https://127.0.0.1.nip.io:18443 xcodebuild test -scheme iOSTests …
///
/// Use a DNS name, not an IP: Info.plist's NSAllowsLocalNetworking exempts IP addresses and
/// .local names from ATS, so only a name exercises what a real server gets. With ATS on for
/// the host, the pinned request fails with -1200 / -9802 "ATS failed system trust": ATS
/// runs its own system-trust check whatever the delegate accepts. Passing needs ATS off for
/// that host (NSAllowsArbitraryLoads, or an NSExceptionDomains entry).
final class PinningATSTests: XCTestCase {
    override func tearDown() {
        DiscoNet.pin = nil
        super.tearDown()
    }

    func testPinnedSelfSignedServerUnderATS() async throws {
        let raw = ProcessInfo.processInfo.environment["DD_PIN_SERVER"] ?? ""
        try XCTSkipIf(raw.isEmpty, "set TEST_RUNNER_DD_PIN_SERVER to run the ATS pinning check")
        let url = try XCTUnwrap(URL(string: raw))

        let cert = try await DiscoNet.fetchCertificate(url)
        XCTAssertFalse(cert.trusted)
        XCTAssertEqual(cert.fingerprint.count, 95)

        // No pin: refused, and not reported as a changed certificate.
        DiscoNet.pin = nil
        do { _ = try await DiscoNet.session.data(from: url); XCTFail("strict session accepted a self-signed server") }
        catch { XCTAssertFalse(DiscoNet.isCertificateChanged(error), "\(error)") }

        // Pinned to the fetched certificate: connects.
        DiscoNet.pin = cert.fingerprint
        let (_, response) = try await DiscoNet.session.data(from: url)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)

        // Pinned to another certificate: refused as a changed certificate.
        DiscoNet.pin = String(repeating: "00:", count: 31) + "00"
        do { _ = try await DiscoNet.session.data(from: url); XCTFail("wrong pin accepted") }
        catch {
            XCTAssertTrue(DiscoNet.isCertificateChanged(error), "\(error)")
            XCTAssertTrue(DiscoNet.explain(error).localizedDescription.hasPrefix("server certificate changed"))
        }
    }
}
