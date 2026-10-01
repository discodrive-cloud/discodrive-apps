import XCTest
@testable import DiscoKit

final class CertificatePinningTests: XCTestCase {
    // openssl req -x509 … -subj "/CN=pin.test/O=Disco Test", valid until 2027-09-29 13:42:30Z (UTCTime).
    static let selfSigned = Data(base64Encoded: "MIIBwDCCAWegAwIBAgIUFH+Tjcc3+vUA2lxhA9z2p2eBP2IwCgYIKoZIzj0EAwIwKDERMA8GA1UEAwwIcGluLnRlc3QxEzARBgNVBAoMCkRpc2NvIFRlc3QwHhcNMjYwOTI5MTM0MjMwWhcNMjcwOTI5MTM0MjMwWjAoMREwDwYDVQQDDAhwaW4udGVzdDETMBEGA1UECgwKRGlzY28gVGVzdDBZMBMGByqGSM49AgEGCCqGSM49AwEHA0IABC/FfHo6fNNsJhuLehDk3hmhfEZ953/WptXblVGAaezX9Apb7SFwnPQuqFr10n48A8zvuaxhH7HuZRetbtMFEaSjbzBtMB0GA1UdDgQWBBTaQkT3MssT8jNJknQgbvi4r2ztOzAfBgNVHSMEGDAWgBTaQkT3MssT8jNJknQgbvi4r2ztOzAPBgNVHRMBAf8EBTADAQH/MBoGA1UdEQQTMBGCCWxvY2FsaG9zdIcEfwAAATAKBggqhkjOPQQDAgNHADBEAiBqjiB8LdLxwcjgtGpezxi0LAets8KqU3IjEl0nvzEYTgIgeg8vy4KO5lwuN8PgnlhYHlcdr7ALRnIXYBSk87hO3RA=")!
    static let selfSignedFingerprint = "DD:67:CB:26:58:14:CD:10:80:6E:87:23:CE:7D:24:6A:CE:19:69:9D:99:A9:E7:4E:5B:E7:A0:AC:93:93:E7:01"
    // CN=leaf.test issued by CN=Disco Test CA, valid until 2059-08-07 13:42:30Z (GeneralizedTime).
    static let caIssued = Data(base64Encoded: "MIIBcjCCARigAwIBAgIUE0VjCnPr7kQPizM7WmRhr4rK5gEwCgYIKoZIzj0EAwIwGDEWMBQGA1UEAwwNRGlzY28gVGVzdCBDQTAgFw0yNjA5MjkxMzQyMzBaGA8yMDU5MDgwNzEzNDIzMFowFDESMBAGA1UEAwwJbGVhZi50ZXN0MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEtTS1WR2qTiMQgfjTo9sDh2iW7V2/0JiY7iJxcHJmuRDjFW9vfuDRBFyu5NLbhgnf3RfYRzqjoK5/O3FOtIyy7KNCMEAwHQYDVR0OBBYEFPmsyBTITA9nFfEGL+EUV5X3u1jjMB8GA1UdIwQYMBaAFP5pDVdexgtNL+3M0om/5IQVI6MoMAoGCCqGSM49BAMCA0gAMEUCIQDSIm+1aBguznRNpjzgX9eAcqAXax5cwC4oWCoUr5xfNwIgY9lubx4Pt1FxumdOZAvNLw0bo55LA3toS0xliCS6ojM=")!
    static let caIssuedFingerprint = "BC:4A:4C:FC:B5:B7:E6:FE:C4:A2:EE:28:F0:AE:2A:56:E1:B0:2F:89:A3:DC:1D:1A:78:08:5A:3A:2B:65:62:38"

    override func tearDown() {
        DiscoNet.pin = nil
        DiscoNet.forgetMismatches()
        MockURLProtocol.failure = nil
        MockURLProtocol.handler = nil
        super.tearDown()
    }

    // MARK: fingerprint format

    func testFingerprintIsOpensslFormat() {
        XCTAssertEqual(DiscoNet.fingerprint(of: Self.selfSigned), Self.selfSignedFingerprint)
        XCTAssertEqual(DiscoNet.fingerprint(of: Self.caIssued), Self.caIssuedFingerprint)
        XCTAssertEqual(DiscoNet.fingerprint(of: Self.selfSigned).count, 95)
        // SHA-256("abc"), the standard test vector.
        XCTAssertEqual(DiscoNet.fingerprint(of: Data("abc".utf8)),
                       "BA:78:16:BF:8F:01:CF:EA:41:41:40:DE:5D:AE:22:23:B0:03:61:A3:96:17:7A:9C:B4:10:FF:61:F2:00:15:AD")
    }

    func testSameFingerprintNormalizes() {
        let fp = Self.selfSignedFingerprint
        XCTAssertTrue(DiscoNet.sameFingerprint(fp, fp))
        XCTAssertTrue(DiscoNet.sameFingerprint(fp, fp.lowercased()))
        XCTAssertTrue(DiscoNet.sameFingerprint(fp, fp.replacingOccurrences(of: ":", with: "")))
        XCTAssertTrue(DiscoNet.sameFingerprint(" " + fp.replacingOccurrences(of: ":", with: " ") + "\n", fp))
        XCTAssertFalse(DiscoNet.sameFingerprint(fp, Self.caIssuedFingerprint))
        // An empty pin must never match anything, not even another empty string.
        XCTAssertFalse(DiscoNet.sameFingerprint("", ""))
        XCTAssertFalse(DiscoNet.sameFingerprint(" : ", ""))
    }

    // MARK: acceptance rule

    func testSystemTrustedChainWinsWhateverThePin() {
        XCTAssertEqual(DiscoNet.decide(systemTrusted: true, leaf: Self.selfSigned, pin: nil), .system)
        XCTAssertEqual(DiscoNet.decide(systemTrusted: true, leaf: Self.selfSigned, pin: Self.caIssuedFingerprint), .system)
    }

    func testUntrustedWithoutPinIsRejected() {
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: Self.selfSigned, pin: nil), .reject)
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: Self.selfSigned, pin: ""), .reject)
    }

    func testUntrustedLeafMatchingThePinIsAccepted() {
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: Self.selfSigned, pin: Self.selfSignedFingerprint), .pinned)
        let loose = Self.selfSignedFingerprint.replacingOccurrences(of: ":", with: "").lowercased()
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: Self.selfSigned, pin: loose), .pinned)
    }

    func testUntrustedLeafNotMatchingThePinIsAChange() {
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: Self.caIssued, pin: Self.selfSignedFingerprint),
                       .changed(expected: Self.selfSignedFingerprint, got: Self.caIssuedFingerprint))
        // No leaf to compare: nothing was presented that the pin could vouch for.
        XCTAssertEqual(DiscoNet.decide(systemTrusted: false, leaf: nil, pin: Self.selfSignedFingerprint), .reject)
    }

    func testChangedErrorNamesBothFingerprints() {
        let e = CertificateChangedError(expected: "AA:BB", got: "CC:DD")
        let text = e.localizedDescription
        XCTAssertTrue(text.hasPrefix("server certificate changed"), text)
        XCTAssertTrue(text.hasPrefix(DiscoNet.certificateChangedMarker), text)
        XCTAssertTrue(text.contains("AA:BB") && text.contains("CC:DD"), text)
    }

    // MARK: pin storage

    func testPinIsStoredAndClearedByEmptyValue() {
        DiscoNet.pin = Self.selfSignedFingerprint
        XCTAssertEqual(DiscoNet.pin, Self.selfSignedFingerprint)
        DiscoNet.pin = ""
        XCTAssertNil(DiscoNet.pin)
    }

    func testPinConcurrentAccess() {
        DispatchQueue.concurrentPerform(iterations: 200) { i in
            if i % 2 == 0 { DiscoNet.pin = "\(i)" } else { _ = DiscoNet.pin }
        }
    }

    func testKeychainServiceForThePin() {
        XCTAssertEqual(KeychainToken.pinService, "org.discodrive.serverpin")
    }

    func testPairingSessionIgnoresTheAccountPin() {
        DiscoNet.pin = Self.selfSignedFingerprint
        // A strict attempt must be strict even while a previous pairing's pin is loaded.
        XCTAssertTrue(DiscoNet.session(pin: nil) !== DiscoNet.session)
        // The account session follows the pin: another pin, or none, is another session.
        XCTAssertTrue(DiscoNet.session === DiscoNet.session(pin: Self.selfSignedFingerprint))
        DiscoNet.pin = nil
        XCTAssertTrue(DiscoNet.session === DiscoNet.session(pin: nil))
        DiscoNet.pin = Self.selfSignedFingerprint
        XCTAssertTrue(DiscoNet.session(pin: "") === DiscoNet.session(pin: nil))
        XCTAssertTrue(DiscoNet.session(pin: Self.selfSignedFingerprint) === DiscoNet.session(pin: Self.selfSignedFingerprint))
        XCTAssertTrue(DiscoNet.session(pin: Self.selfSignedFingerprint) !== DiscoNet.session(pin: Self.caIssuedFingerprint))
    }

    // MARK: explaining a rejected connection

    func testRejectedConnectionIsExplainedAsChangedCertificate() {
        DiscoNet.recordMismatch(host: "files.test", port: 8443, expected: "AA", got: "BB")
        let failing = URLError(.cancelled, userInfo: [NSURLErrorFailingURLErrorKey: URL(string: "https://files.test:8443/sync/changes")!])
        let explained = DiscoNet.explain(failing)
        XCTAssertEqual(explained as? CertificateChangedError, CertificateChangedError(expected: "AA", got: "BB"))
        XCTAssertTrue(DiscoNet.isCertificateChanged(failing))

        // Another host, or the same host on another port, is not affected.
        let other = URLError(.cancelled, userInfo: [NSURLErrorFailingURLErrorKey: URL(string: "https://files.test/x")!])
        XCTAssertTrue(DiscoNet.explain(other) is URLError)
        // A plain system refusal is not a change, even for a host with a recorded mismatch.
        XCTAssertTrue(DiscoNet.explain(URLError(.serverCertificateUntrusted, userInfo: [NSURLErrorFailingURLErrorKey: URL(string: "https://files.test:8443/")!])) is URLError)
        // Unrelated failures keep their meaning.
        XCTAssertTrue(DiscoNet.explain(URLError(.notConnectedToInternet, userInfo: [NSURLErrorFailingURLErrorKey: URL(string: "https://files.test:8443/")!])) is URLError)

        // An accepted handshake with the host clears the record.
        DiscoNet.recordAccepted(host: "files.test", port: 8443)
        XCTAssertTrue(DiscoNet.explain(failing) is URLError)
    }

    func testExplainUsesTheGivenURLWhenTheErrorHasNone() {
        DiscoNet.recordMismatch(host: "files.test", port: 443, expected: "AA", got: "BB")
        let bare = URLError(.cancelled)
        XCTAssertTrue(DiscoNet.explain(bare) is URLError)
        XCTAssertTrue(DiscoNet.explain(bare, for: URL(string: "https://FILES.test/pair/token")!) is CertificateChangedError)
    }

    // A changed certificate does not heal by waiting: polling stops at once rather than
    // retrying through the network grace window.
    func testPollStopsOnChangedCertificate() async throws {
        let base = URL(string: "https://files.test")!
        DiscoNet.recordMismatch(host: "files.test", port: 443, expected: "AA", got: "BB")
        MockURLProtocol.failure = { req in URLError(.cancelled, userInfo: [NSURLErrorFailingURLErrorKey: req.url!]) }
        let p = Pairing(baseURL: base, session: MockURLProtocol.session())
        do {
            _ = try await p.poll(deviceCode: "DC", interval: .milliseconds(1), networkGrace: .seconds(60))
            XCTFail("expected the changed certificate to end polling")
        } catch let e as CertificateChangedError {
            XCTAssertEqual(e, CertificateChangedError(expected: "AA", got: "BB"))
        }
    }

    // MARK: certificate details for the trust dialog

    func testCertificateInfoForSelfSignedLeaf() throws {
        let info = try XCTUnwrap(CertificateInfo(host: "pin.test:8443", der: Self.selfSigned, trusted: false))
        XCTAssertEqual(info.host, "pin.test:8443")
        XCTAssertEqual(info.fingerprint, Self.selfSignedFingerprint)
        XCTAssertEqual(info.subject, "pin.test")
        XCTAssertEqual(info.issuer, "pin.test")
        XCTAssertTrue(info.selfSigned)
        XCTAssertFalse(info.trusted)
        XCTAssertEqual(info.notAfter, ISO8601DateFormatter().date(from: "2027-09-29T13:42:30Z"))
    }

    func testCertificateInfoForCAIssuedLeaf() throws {
        let info = try XCTUnwrap(CertificateInfo(host: "leaf.test", der: Self.caIssued, trusted: true))
        XCTAssertEqual(info.subject, "leaf.test")
        XCTAssertEqual(info.issuer, "Disco Test CA")
        XCTAssertFalse(info.selfSigned)
        XCTAssertTrue(info.trusted)
        XCTAssertEqual(info.notAfter, ISO8601DateFormatter().date(from: "2059-08-07T13:42:30Z"))
    }

    func testCertificateInfoRejectsGarbage() {
        XCTAssertNil(CertificateInfo(host: "x", der: Data([0x30, 0x03, 0x02, 0x01]), trusted: false))
        XCTAssertNil(CertificateInfo(host: "x", der: Data(), trusted: false))
    }

    func testTrustDialogStringsInEveryLanguage() {
        let keys = ["pairing.certTitle", "pairing.certHint", "pairing.certTrust", "pairing.certSelfSigned",
                    "pairing.certChanged", "pairing.certHost", "pairing.certFingerprint", "pairing.certSubject",
                    "pairing.certIssuer", "pairing.certExpires"]
        for key in keys {
            for lang in L10n.supported {
                XCTAssertFalse(L10n.table[key]?[lang]?.isEmpty ?? true, "\(key) missing in \(lang)")
            }
        }
        XCTAssertEqual(L10n.t("pairing.certHint", "en"), "Only trust this if it matches the fingerprint of your server's certificate.")
    }

    func testFetchCertificateRefusesPlainHTTP() async {
        do {
            _ = try await DiscoNet.fetchCertificate(URL(string: "http://files.test")!)
            XCTFail("expected http:// to be refused")
        } catch {}
    }
}

/// The whole path against a real TLS server with a self-signed certificate. Skipped unless
/// one is running, e.g.:
///
///   openssl s_server -accept 8443 -cert self.pem -key self.key -www &
///   DD_PIN_SERVER=https://127.0.0.1:8443 swift test --filter LivePinning
final class LivePinningTests: XCTestCase {
    func testFetchThenPinnedRequest() async throws {
        let raw = ProcessInfo.processInfo.environment["DD_PIN_SERVER"] ?? ""
        try XCTSkipIf(raw.isEmpty, "set DD_PIN_SERVER to run the live pinning checks")
        let url = URL(string: raw)!

        let cert = try await DiscoNet.fetchCertificate(url)
        XCTAssertFalse(cert.trusted)
        XCTAssertTrue(cert.selfSigned)
        XCTAssertEqual(cert.fingerprint.count, 95)

        // Strict: refused.
        do { _ = try await DiscoNet.session(pin: nil).data(from: url); XCTFail("strict session accepted a self-signed server") }
        catch { XCTAssertFalse(DiscoNet.isCertificateChanged(error)) }

        // Pinned to what was fetched: accepted.
        let (_, resp) = try await DiscoNet.session(pin: cert.fingerprint).data(from: url)
        XCTAssertEqual((resp as? HTTPURLResponse)?.statusCode, 200)

        // Pinned to something else: refused, and explained as a changed certificate.
        let wrong = String(repeating: "00:", count: 31) + "00"
        do { _ = try await DiscoNet.session(pin: wrong).data(from: url); XCTFail("wrong pin accepted") }
        catch {
            // The failing URL travels with the error: no URL needed to explain it.
            XCTAssertTrue(DiscoNet.isCertificateChanged(error), "\(error)")
            let explained = DiscoNet.explain(error, for: url)
            XCTAssertTrue(explained.localizedDescription.hasPrefix("server certificate changed"), "\(error)")
            XCTAssertTrue(explained.localizedDescription.contains(cert.fingerprint))
        }

        // The global session follows DiscoNet.pin.
        DiscoNet.pin = cert.fingerprint
        defer { DiscoNet.pin = nil }
        let (_, resp2) = try await DiscoNet.session.data(from: url)
        XCTAssertEqual((resp2 as? HTTPURLResponse)?.statusCode, 200)

        // Dropping the pin must drop what was accepted under it: the next request through
        // the same session is refused again, not served from a cached connection.
        DiscoNet.pin = nil
        do { _ = try await DiscoNet.session.data(from: url); XCTFail("session kept trusting the server after the pin was cleared") }
        catch { XCTAssertFalse(DiscoNet.isCertificateChanged(error), "\(error)") }

        // And a wrong pin set afterwards is reported as a changed certificate.
        DiscoNet.pin = wrong
        do { _ = try await DiscoNet.session.data(from: url); XCTFail("session accepted a wrong pin") }
        catch { XCTAssertTrue(DiscoNet.isCertificateChanged(error), "\(error)") }
    }
}
