import Foundation
import Kfmobile

// Thin wrapper over the gomobile-generated Kfmobile API. Free functions take an NSError pointer
// (not Swift throws); MobileClient methods throw. Every call blocks — invoke off the main thread.
enum SyncError: Error { case unknown }

enum SyncCore {
    // `pin` ("" = strict) is the fingerprint of the server certificate the user trusted.
    static func pairBegin(server: String, name: String, kind: String, pin: String) throws -> MobilePairing {
        var err: NSError?
        guard let p = MobilePairBegin(server, name, kind, pin, &err) else { throw err ?? SyncError.unknown }
        return p
    }

    static func pairAwait(server: String, deviceCode: String, intervalSec: Int, pin: String) throws -> String {
        var err: NSError?
        let token = MobilePairAwait(server, deviceCode, intervalSec, pin, &err)
        if let err { throw err }
        return token
    }

    static func newClient(server: String, token: String, syncDir: String, dbPath: String, pin: String) throws -> MobileClient {
        var err: NSError?
        guard let c = MobileNew(server, token, syncDir, dbPath, pin, &err) else { throw err ?? SyncError.unknown }
        return c
    }

    /// Reads the server's certificate without sending a request (for the trust dialog).
    static func fetchCertificate(server: String) throws -> MobileCertificate {
        var err: NSError?
        guard let c = MobileFetchCertificate(server, &err) else { throw err ?? SyncError.unknown }
        return c
    }

    /// The error means the server now presents a certificate other than the trusted one.
    static func isCertificateChanged(_ error: Error) -> Bool {
        error.localizedDescription.contains(MobileCertificateChangedMarker)
    }
}
