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

    private var client: MobileClient?
    private(set) var serverURL = ""
    private(set) var insecure = false

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
        insecure = Keychain.get("insecure") == "1"
        try? FileManager.default.createDirectory(at: syncDirURL, withIntermediateDirectories: true)
        if let token = Keychain.get("deviceToken"), !serverURL.isEmpty {
            openClient(server: serverURL, token: token, insecure: insecure)
        }
    }

    private func openClient(server: String, token: String, insecure: Bool) {
        do {
            client = try SyncCore.newClient(server: server, token: token,
                                            syncDir: syncDirURL.path, dbPath: stateDBPath, insecure: insecure)
            paired = true
        } catch { lastError = error.localizedDescription }
    }

    func startPairing(server: String, insecure: Bool) async -> MobilePairing? {
        working = true; lastError = nil
        do {
            let p = try await runOff { try SyncCore.pairBegin(server: server, name: Self.deviceName, kind: "ios", insecure: insecure) }
            pendingUserCode = p.userCode
            return p
        } catch {
            lastError = error.localizedDescription; working = false; return nil
        }
    }

    func finishPairing(server: String, pairing: MobilePairing, insecure: Bool) async {
        do {
            let token = try await runOff {
                try SyncCore.pairAwait(server: server, deviceCode: pairing.deviceCode,
                                       intervalSec: pairing.intervalSeconds, insecure: insecure)
            }
            Keychain.set(server, for: "serverURL")
            Keychain.set(insecure ? "1" : "0", for: "insecure")
            Keychain.set(token, for: "deviceToken")
            serverURL = server; self.insecure = insecure
            openClient(server: server, token: token, insecure: insecure)
        } catch {
            lastError = error.localizedDescription
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
            lastError = error.localizedDescription
        }
        if let st = client.status() {
            stateText = st.state
            lastSyncUnix = st.lastSyncUnix
            if !st.lastError.isEmpty { lastError = st.lastError }
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
        Keychain.set(nil, for: "insecure")
        paired = false; stateText = "idle"; lastSyncUnix = 0; lastError = nil
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
