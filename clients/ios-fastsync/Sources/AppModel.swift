import SwiftUI
import UIKit
import BackgroundTasks
import Kfmobile

@MainActor
final class AppModel: ObservableObject {
    // Background App Refresh task identifier (must match Info.plist BGTaskSchedulerPermittedIdentifiers).
    static let bgTaskID = "org.discodrive.fastsync.refresh"
    @Published var paired = false
    @Published var working = false
    @Published var stateText = "idle"
    @Published var lastSyncUnix: Int64 = 0
    @Published var lastError: String?
    @Published var pendingUserCode: String?
    // Where the first pass after pairing moved the folder's previous contents, if any.
    @Published var setAside: String?
    /// The server's certificate, offered for trust after a strict pairing attempt failed on it.
    @Published var pendingCertificate: MobileCertificate?
    /// The server `pendingCertificate` was read from: a trusted pin is only used for it.
    private(set) var certificateServer: String?

    private var client: MobileClient?
    private(set) var serverURL = ""
    // Fingerprint of the certificate trusted at pairing; "" = strict system validation.
    private(set) var pin = ""

    static let deviceName = UIDevice.current.name

    var syncDirURL: URL {
        FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Sync", isDirectory: true)
    }

    private var stateDBPath: String {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("discodrive", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.appendingPathComponent("state.db").path
    }

    init() {
        serverURL = Keychain.get("serverURL") ?? ""
        pin = Keychain.get("serverPin") ?? ""
        // The old switch that turned certificate checks off is gone; so is its setting.
        Keychain.set(nil, for: "insecure")
        try? FileManager.default.createDirectory(at: syncDirURL, withIntermediateDirectories: true)
        if let token = Keychain.get("deviceToken"), !serverURL.isEmpty {
            openClient(server: serverURL, token: token, pin: pin)
        }
    }

    private func openClient(server: String, token: String, pin: String) {
        do {
            client = try SyncCore.newClient(server: server, token: token,
                                            syncDir: syncDirURL.path, dbPath: stateDBPath, pin: pin)
            paired = true
        } catch { lastError = Self.message(error.localizedDescription) }
    }

    /// `pin` is "" for the strict first attempt, else the fingerprint of the certificate just
    /// fetched from `server` and trusted by the user. A strict attempt that fails on an
    /// untrusted certificate sets `pendingCertificate` instead of an error.
    func startPairing(server: String, pin: String = "") async -> MobilePairing? {
        working = true; lastError = nil
        do {
            let p = try await runOff { try SyncCore.pairBegin(server: server, name: Self.deviceName, kind: "ios", pin: pin) }
            pendingUserCode = p.userCode
            return p
        } catch {
            if pin.isEmpty, let cert = try? await runOff({ try SyncCore.fetchCertificate(server: server) }), !cert.trusted {
                certificateServer = server
                pendingCertificate = cert
            } else {
                lastError = Self.message(error.localizedDescription)
            }
            working = false; return nil
        }
    }

    func finishPairing(server: String, pairing: MobilePairing, pin: String = "") async {
        do {
            let token = try await runOff {
                try SyncCore.pairAwait(server: server, deviceCode: pairing.deviceCode,
                                       intervalSec: pairing.intervalSeconds, pin: pin)
            }
            Keychain.set(server, for: "serverURL")
            Keychain.set(pin.isEmpty ? nil : pin, for: "serverPin")
            Keychain.set(token, for: "deviceToken")
            serverURL = server; self.pin = pin
            openClient(server: server, token: token, pin: pin)
        } catch {
            lastError = Self.message(error.localizedDescription)
        }
        pendingUserCode = nil
        working = false
    }

    func syncNow() async {
        guard let client else { return }
        working = true; lastError = nil; stateText = "syncing"
        do {
            try await runOff { try client.syncOnce() }
        } catch {
            lastError = Self.message(error.localizedDescription)
        }
        if let st = client.status() {
            stateText = st.state
            lastSyncUnix = st.lastSyncUnix
            if !st.lastError.isEmpty { lastError = Self.message(st.lastError) }
            if !st.setAside.isEmpty { setAside = st.setAside }
        }
        working = false
    }

    func unpair() {
        try? client?.close()
        client = nil
        // The index goes with the pairing. Left behind, it describes files this device no
        // longer has — pair again once the sync folder is gone (a reinstall keeps neither in
        // step) and every one of them reads as locally deleted, which the push then carries
        // to the server. That is how an Android device emptied a whole vault.
        let db = stateDBPath
        for path in [db, db + "-wal", db + "-shm"] {
            try? FileManager.default.removeItem(atPath: path)
        }
        Keychain.set(nil, for: "deviceToken")
        Keychain.set(nil, for: "serverURL")
        Keychain.set(nil, for: "serverPin")
        pin = ""
        paired = false; stateText = "idle"; lastSyncUnix = 0; lastError = nil
    }

    /// Error text for the screen; a changed certificate is explained before the details.
    static func message(_ text: String) -> String {
        guard text.contains(MobileCertificateChangedMarker) else { return text }
        return "The server certificate changed. If you did not replace it, someone may be intercepting the connection. Pair again to trust the new certificate. (\(text))"
    }

    private func runOff<T>(_ body: @escaping () throws -> T) async throws -> T {
        try await Task.detached(priority: .userInitiated) { try body() }.value
    }

    // Ask iOS to wake the app for a sync no sooner than ~20 min from now. The OS decides the
    // actual time (app usage, battery, network); it does not run while the app is force-quit.
    // nonisolated so it can be called from the background-task handler and scenePhase observer.
    nonisolated func scheduleBackgroundSync() {
        let req = BGAppRefreshTaskRequest(identifier: Self.bgTaskID)
        req.earliestBeginDate = Date(timeIntervalSinceNow: 20 * 60)
        try? BGTaskScheduler.shared.submit(req)
    }
}
