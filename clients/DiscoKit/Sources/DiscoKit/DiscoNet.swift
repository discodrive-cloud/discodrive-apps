import Foundation
import CryptoKit
import Security
import os

/// A server certificate that no longer matches the one the user trusted at pairing.
public struct CertificateChangedError: LocalizedError, Equatable, Sendable {
    public let expected: String
    public let got: String
    public init(expected: String, got: String) { self.expected = expected; self.got = got }
    // Same prefix as every other client, so UIs can match on it.
    public var errorDescription: String? { "\(DiscoNet.certificateChangedMarker): expected \(expected), got \(got)" }
}

/// What the trust dialog shows about a server's leaf certificate.
public struct CertificateInfo: Sendable, Equatable {
    public let host: String          // URL host, with the port when the URL names one
    public let fingerprint: String   // SHA-256 of the DER, uppercase hex with colons
    public let subject: String       // common name, else the whole name
    public let issuer: String
    public let notAfter: Date?
    public let selfSigned: Bool      // issuer name equals subject name
    public let trusted: Bool         // the system trusts the chain for this host

    public init(host: String, fingerprint: String, subject: String, issuer: String,
                notAfter: Date?, selfSigned: Bool, trusted: Bool) {
        self.host = host; self.fingerprint = fingerprint; self.subject = subject; self.issuer = issuer
        self.notAfter = notAfter; self.selfSigned = selfSigned; self.trusted = trusted
    }

    /// Reads the details from the certificate's DER; nil when it is not an X.509 certificate.
    public init?(host: String, der: Data, trusted: Bool) {
        guard let parsed = X509Summary(der: der) else { return nil }
        self.init(host: host, fingerprint: DiscoNet.fingerprint(of: der), subject: parsed.subject,
                  issuer: parsed.issuer, notAfter: parsed.notAfter, selfSigned: parsed.selfSigned, trusted: trusted)
    }
}

public enum DiscoNet {
    /// Starts the error text of a rejected, changed certificate on every platform.
    public static let certificateChangedMarker = "server certificate changed"

    /// What to do with a server's certificate, per the acceptance rule.
    public enum TrustDecision: Equatable, Sendable {
        case system                                  // chain verifies with the system roots
        case pinned                                  // untrusted, but it is the certificate the user trusted
        case reject                                  // untrusted and nothing vouches for it
        case changed(expected: String, got: String)  // a pin exists and this is not it
    }

    /// The acceptance rule, kept free of Security types so it can be tested directly.
    /// Hostname and expiry are not checked on the pinned branch: the user trusted that
    /// exact certificate.
    public static func decide(systemTrusted: Bool, leaf: Data?, pin: String?) -> TrustDecision {
        if systemTrusted { return .system }
        guard let pin, !normalize(pin).isEmpty, let leaf else { return .reject }
        let got = fingerprint(of: leaf)
        return sameFingerprint(pin, got) ? .pinned : .changed(expected: pin, got: got)
    }

    /// SHA-256 over the certificate DER, formatted like `openssl x509 -fingerprint -sha256`.
    public static func fingerprint(of der: Data) -> String {
        SHA256.hash(data: der).map { String(format: "%02X", $0) }.joined(separator: ":")
    }

    /// Compares two fingerprints ignoring colons, whitespace and case. Empty never matches.
    public static func sameFingerprint(_ a: String, _ b: String) -> Bool {
        let x = normalize(a)
        return !x.isEmpty && x == normalize(b)
    }

    static func normalize(_ pin: String) -> String {
        String(pin.unicodeScalars.filter { $0 != ":" && !CharacterSet.whitespacesAndNewlines.contains($0) }).uppercased()
    }

    // MARK: state

    private struct State {
        var pin: String?
        var pinnedSessions: [String: URLSession] = [:]
        var mismatches: [String: CertificateChangedError] = [:]
    }
    private static let state = OSAllocatedUnfairLock(initialState: State())

    /// The pin of the paired account, which selects `session`. The apps and the File
    /// Provider extension set it from the keychain before creating their API client; empty
    /// means none.
    public static var pin: String? {
        get { state.withLock { $0.pin } }
        set { state.withLock { $0.pin = newValue.flatMap { normalize($0).isEmpty ? nil : $0 } } }
    }

    /// The session for the paired account's requests: strict system validation, plus the
    /// certificate `pin` names. Read it after setting `pin`; a client keeps the one it got.
    ///
    /// Each pin gets a session of its own rather than one session whose delegate follows the
    /// pin: a URLSession resumes TLS sessions without asking its delegate again (even after
    /// `reset`), so a certificate accepted under one pin would stay accepted under the next.
    public static var session: URLSession { session(pin: pin) }

    private static let strictSession = URLSession(configuration: .default, delegate: PinningDelegate(pin: nil), delegateQueue: nil)

    /// Strict for nil, else a session that accepts, beyond what the system trusts, exactly the
    /// certificate with fingerprint `pin`. Pairing uses it directly: the strict first attempt
    /// must not inherit the current pairing's pin.
    public static func session(pin: String?) -> URLSession {
        guard let pin, !normalize(pin).isEmpty else { return strictSession }
        return state.withLock { s in
            if let existing = s.pinnedSessions[normalize(pin)] { return existing }
            let made = URLSession(configuration: .default, delegate: PinningDelegate(pin: pin), delegateQueue: nil)
            s.pinnedSessions[normalize(pin)] = made
            return made
        }
    }

    // Redirects can replay tokens and upload bodies; initial URL validation alone
    // cannot protect them. Permit path changes only within the original origin.
    static func allowsRedirect(from original: URL, to target: URL) -> Bool {
        guard let scheme = original.scheme?.lowercased(),
              let host = original.host?.lowercased(),
              target.scheme?.lowercased() == scheme,
              target.host?.lowercased() == host,
              target.user == nil, target.password == nil else { return false }
        let defaultPort = scheme == "https" ? 443 : 80
        return (original.port ?? defaultPort) == (target.port ?? defaultPort)
    }

    // MARK: explaining a rejected connection

    // URLSession cannot fail a task with an error of ours: a cancelled challenge surfaces as
    // a plain URLError. The delegate notes the mismatch per host, and `explain` puts it back.
    static func key(host: String, port: Int) -> String { "\(host.lowercased()):\(port)" }

    static func key(for url: URL) -> String? {
        guard let host = url.host, !host.isEmpty else { return nil }
        let port = url.port ?? (url.scheme?.lowercased() == "http" ? 80 : 443)
        return key(host: host, port: port)
    }

    static func recordMismatch(host: String, port: Int, expected: String, got: String) {
        state.withLock { $0.mismatches[key(host: host, port: port)] = CertificateChangedError(expected: expected, got: got) }
    }

    static func recordAccepted(host: String, port: Int) {
        state.withLock { _ = $0.mismatches.removeValue(forKey: key(host: host, port: port)) }
    }

    static func forgetMismatches() { state.withLock { $0.mismatches = [:] } }

    // What a task fails with when the delegate cancels a mismatching challenge. A system
    // refusal (-1202 and friends) comes from a session without that pin and is not a change.
    private static let rejectionCodes: Set<URLError.Code> = [.cancelled]

    /// Turns a connection refused because the pinned certificate changed into a
    /// `CertificateChangedError`; any other error comes back unchanged. `url` stands in
    /// when the error does not carry the failing URL.
    public static func explain(_ error: Error, for url: URL? = nil) -> Error {
        if error is CertificateChangedError { return error }
        guard let u = error as? URLError, rejectionCodes.contains(u.code),
              let target = u.failingURL ?? url, let k = key(for: target) else { return error }
        return state.withLock { $0.mismatches[k] } ?? error
    }

    public static func isCertificateChanged(_ error: Error, for url: URL? = nil) -> Bool {
        explain(error, for: url) is CertificateChangedError
    }

    // MARK: reading a server's certificate

    /// Reads the server's leaf certificate: a TLS handshake whose challenge is captured and
    /// then cancelled, so no request is ever sent. https only.
    public static func fetchCertificate(_ url: URL) async throws -> CertificateInfo {
        guard url.scheme?.lowercased() == "https", let host = url.host, !host.isEmpty else {
            throw URLError(.unsupportedURL)
        }
        let grabber = CertificateGrabber()
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 30
        config.timeoutIntervalForResource = 30
        let session = URLSession(configuration: config, delegate: grabber, delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        var request = URLRequest(url: url)
        request.httpMethod = "HEAD"
        var failure: Error?
        do { _ = try await session.data(for: request) } catch { failure = error }
        guard let (trusted, der) = grabber.result else { throw failure ?? URLError(.secureConnectionFailed) }
        let label = url.port.map { "\(host):\($0)" } ?? host
        if let info = CertificateInfo(host: label, der: der, trusted: trusted) { return info }
        // Not parseable by the reader above: the fingerprint is what matters.
        let summary = SecCertificateCreateWithData(nil, der as CFData).flatMap { SecCertificateCopySubjectSummary($0) as String? } ?? ""
        return CertificateInfo(host: label, fingerprint: fingerprint(of: der), subject: summary, issuer: "",
                               notAfter: nil, selfSigned: false, trusted: trusted)
    }

    static func leafDER(of trust: SecTrust) -> Data? {
        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else { return nil }
        return SecCertificateCopyData(leaf) as Data
    }
}

// Applies the acceptance rule, with one fixed pin, to every connection of its session.
public final class PinningDelegate: NSObject, URLSessionTaskDelegate, Sendable {
    private let pin: String?
    public init(pin: String?) { self.pin = pin }

    public func urlSession(_ session: URLSession, task: URLSessionTask,
                    willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                    completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        guard let original = task.originalRequest?.url, let target = request.url,
              DiscoNet.allowsRedirect(from: original, to: target) else {
            completionHandler(nil); return
        }
        completionHandler(request)
    }

    public func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodServerTrust, let trust = space.serverTrust else {
            completionHandler(.performDefaultHandling, nil); return
        }
        let trusted = SecTrustEvaluateWithError(trust, nil)
        switch DiscoNet.decide(systemTrusted: trusted, leaf: DiscoNet.leafDER(of: trust), pin: pin) {
        case .system:
            DiscoNet.recordAccepted(host: space.host, port: space.port)
            completionHandler(.performDefaultHandling, nil)
        case .pinned:
            DiscoNet.recordAccepted(host: space.host, port: space.port)
            completionHandler(.useCredential, URLCredential(trust: trust))
        case .reject:
            // The system's own evaluation has just failed; let it refuse with its usual error.
            completionHandler(.performDefaultHandling, nil)
        case .changed(let expected, let got):
            DiscoNet.recordMismatch(host: space.host, port: space.port, expected: expected, got: got)
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }
}

// Captures the first server-trust challenge of a session and cancels it.
private final class CertificateGrabber: NSObject, URLSessionDelegate, Sendable {
    private let captured = OSAllocatedUnfairLock<(Bool, Data)?>(initialState: nil)
    var result: (Bool, Data)? { captured.withLock { $0 } }

    func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                    completionHandler: @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        if challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
           let trust = challenge.protectionSpace.serverTrust, let der = DiscoNet.leafDER(of: trust) {
            let trusted = SecTrustEvaluateWithError(trust, nil)
            captured.withLock { if $0 == nil { $0 = (trusted, der) } }
        }
        completionHandler(.cancelAuthenticationChallenge, nil)
    }
}

// The few fields of an X.509 certificate the trust dialog shows, read straight from the
// DER: iOS has no public API for the issuer or the expiry date before iOS 18.
struct X509Summary {
    let subject: String
    let issuer: String
    let notAfter: Date?
    let selfSigned: Bool

    init?(der: Data) {
        let b = [UInt8](der)
        var outer = DERReader(b, 0..<b.count)
        guard let cert = outer.next(), cert.tag == 0x30 else { return nil }
        var certBody = DERReader(b, cert.content)
        guard let tbs = certBody.next(), tbs.tag == 0x30 else { return nil }
        var r = DERReader(b, tbs.content)
        guard var field = r.next() else { return nil }
        if field.tag == 0xA0 { guard let serial = r.next() else { return nil }; field = serial }   // explicit version
        guard field.tag == 0x02,
              let sigAlg = r.next(), sigAlg.tag == 0x30,
              let issuer = r.next(), issuer.tag == 0x30,
              let validity = r.next(), validity.tag == 0x30,
              let subject = r.next(), subject.tag == 0x30 else { return nil }
        var v = DERReader(b, validity.content)
        _ = v.next()
        notAfter = v.next().flatMap { Self.time(b, $0) }
        self.subject = Self.name(b, subject.content)
        self.issuer = Self.name(b, issuer.content)
        selfSigned = b[issuer.whole] == b[subject.whole]
    }

    // The common name, or every attribute value joined when there is none.
    private static func name(_ b: [UInt8], _ range: Range<Int>) -> String {
        var cn: String?, all: [String] = []
        var sets = DERReader(b, range)
        while let set = sets.next() {
            guard set.tag == 0x31 else { continue }
            var attrs = DERReader(b, set.content)
            while let attr = attrs.next() {
                guard attr.tag == 0x30 else { continue }
                var parts = DERReader(b, attr.content)
                guard let oid = parts.next(), oid.tag == 0x06, let value = parts.next(),
                      let text = string(b, value) else { continue }
                if Array(b[oid.content]) == [0x55, 0x04, 0x03], cn == nil { cn = text }   // 2.5.4.3, commonName
                all.append(text)
            }
        }
        return cn ?? all.joined(separator: ", ")
    }

    private static func string(_ b: [UInt8], _ el: DERReader.Element) -> String? {
        let bytes = Array(b[el.content])
        switch el.tag {
        case 0x0C, 0x13, 0x16, 0x14: return String(decoding: bytes, as: UTF8.self)
        case 0x1E: return String(data: Data(bytes), encoding: .utf16BigEndian)
        default: return nil
        }
    }

    private static func time(_ b: [UInt8], _ el: DERReader.Element) -> Date? {
        var text = String(decoding: b[el.content], as: UTF8.self)
        guard text.hasSuffix("Z") else { return nil }
        if el.tag == 0x17 {   // UTCTime: two-digit year, 50–99 are the 1900s
            guard let yy = Int(text.prefix(2)) else { return nil }
            text = (yy >= 50 ? "19" : "20") + text
        } else if el.tag != 0x18 { return nil }
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        f.dateFormat = "yyyyMMddHHmmss'Z'"
        return f.date(from: text)
    }
}

struct DERReader {
    struct Element { let tag: UInt8; let content: Range<Int>; let whole: Range<Int> }
    let b: [UInt8]
    var pos: Int
    let end: Int
    init(_ b: [UInt8], _ range: Range<Int>) { self.b = b; pos = range.lowerBound; end = range.upperBound }

    mutating func next() -> Element? {
        guard pos + 2 <= end else { return nil }
        let start = pos
        let tag = b[pos]
        var length = Int(b[pos + 1])
        pos += 2
        if length & 0x80 != 0 {
            let n = length & 0x7F
            guard (1...4).contains(n), pos + n <= end else { pos = end; return nil }
            length = 0
            for _ in 0..<n { length = length << 8 | Int(b[pos]); pos += 1 }
        }
        guard pos + length <= end else { pos = end; return nil }
        let content = pos..<(pos + length)
        pos += length
        return Element(tag: tag, content: content, whole: start..<pos)
    }
}
