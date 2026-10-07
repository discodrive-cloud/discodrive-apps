import BackgroundTasks
import CryptoKit
import DiscoKit
import Foundation
import UIKit

@MainActor
final class FullSyncController: ObservableObject {
    static let taskID = "org.discodrive.ios.fullsync"
    @Published private(set) var enabled = UserDefaults.standard.bool(forKey: "fullSync.enabled")
    @Published private(set) var busy = false
    @Published private(set) var activity = SyncActivity()
    @Published private(set) var activityError = ""
    @Published private(set) var statusKey = "fullSync.stopped"
    @Published private(set) var folder: URL?
    private weak var app: AppState?
    private var running = false
    private var foreground = false
    private var backgroundGrant = false
    private var polling: Task<Void, Never>?

    @Published private(set) var loggingEnabled = UserDefaults.standard.bool(forKey: "diagnostics.enabled")
    var logURL: URL { FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent("Logs/sync.log") }
    func setLogging(_ enabled: Bool) {
        let result = (enabled ? logURL.path : "").withCString { DDFullSyncSetLogPath($0) }
        if let result {
            DDFullSyncFree(result)
            loggingEnabled = false
            UserDefaults.standard.set(false, forKey: "diagnostics.enabled")
            app?.lastError = app?.t("diagnostics.failed")
            return
        }
        loggingEnabled = enabled
        UserDefaults.standard.set(enabled, forKey: "diagnostics.enabled")
    }

    func attach(_ app: AppState) {
        self.app = app
        setLogging(loggingEnabled)
    }

    func registerBackgroundTask() {
        // The handler inherits MainActor isolation. A nil queue lets BGTaskScheduler
        // invoke it on its worker queue and traps before the inner Task can hop actors.
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.taskID, using: .main) { task in
            guard let task = task as? BGProcessingTask else { task.setTaskCompleted(success: false); return }
            Task { @MainActor in
                let work = Task { await self.backgroundPass() }
                task.expirationHandler = { @Sendable in work.cancel() }
                let success = await work.value
                task.setTaskCompleted(success: success)
            }
        }
    }

    private func schedule() {
        guard enabled else { return }
        let request = BGProcessingTaskRequest(identifier: Self.taskID)
        request.requiresNetworkConnectivity = true
        request.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
        try? BGTaskScheduler.shared.submit(request)
    }

    func setEnabled(_ value: Bool) async {
        guard !busy else { return }
        if value {
            do {
                let docs = try FileManager.default.url(for: .documentDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
                let url = docs.appendingPathComponent("Sync", isDirectory: true)
                try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
                folder = url
            } catch { statusKey = "fullSync.folderError"; return }
        }
        enabled = value
        UserDefaults.standard.set(value, forKey: "fullSync.enabled")
        if value { schedule(); await start() }
        else { BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID); await stop() }
    }

    func resume() async {
        foreground = true
        if enabled { await setEnabled(true) }
    }

    func suspend() async {
        foreground = false
        schedule()
        if !backgroundGrant {
            let allowance = UIApplication.shared.beginBackgroundTask(withName: "Finish synchronization")
            await stop()
            if allowance != .invalid { UIApplication.shared.endBackgroundTask(allowance) }
        }
    }

    func logout() async {
        enabled = false
        UserDefaults.standard.set(false, forKey: "fullSync.enabled")
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
        await stop()
        FullSyncPairing.invalidate()
    }

    private func start() async {
        guard enabled, !running, !busy, foreground || backgroundGrant,
              let app, app.paired, let server = app.serverURL, let folder else { return }
        busy = true; defer { busy = false }
        do {
            guard let token = try KeychainToken.loadShared(service: KeychainToken.tokenService) else { throw CocoaError(.fileReadNoPermission) }
            let pairing = SHA256.hash(data: Data((server.absoluteString + "\n" + token).utf8))
                .map { String(format: "%02x", $0) }.joined()
            let docs = folder.deletingLastPathComponent()
            statusKey = "fullSync.preparing"
            _ = try await Task.detached {
                try FullSyncPairing.prepare(folder: folder, backups: docs.appendingPathComponent("Sync Backups", isDirectory: true), identity: pairing)
            }.value
            guard enabled, app.paired, app.serverURL == server, !Task.isCancelled else { return }
            let attributes = try FileManager.default.attributesOfItem(atPath: folder.path)
            guard let volume = attributes[.systemNumber], let inode = attributes[.systemFileNumber] else { throw CocoaError(.fileReadUnknown) }
            let identity = "\(server.absoluteString)\n\(token)\n\(volume)\n\(inode)"
            let key = SHA256.hash(data: Data(identity.utf8)).map { String(format: "%02x", $0) }.joined()
            let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
            let state = support.appendingPathComponent("FullSync/\(key)", isDirectory: true)
            try FileManager.default.createDirectory(at: state, withIntermediateDirectories: true)
            _ = try await Task.detached {
                try FullSyncStorage.prepare(folder: folder, state: state, backups: docs.appendingPathComponent("Sync Backups", isDirectory: true))
            }.value
            guard enabled, app.paired, app.serverURL == server, foreground || backgroundGrant, !Task.isCancelled else { return }
            // The certificate trusted at pairing ("" = strict), the same the app itself accepts.
            let pin = try KeychainToken.loadShared(service: KeychainToken.pinService) ?? ""
            let data = try JSONSerialization.data(withJSONObject: ["Server": server.absoluteString, "Token": token,
                "Root": folder.path, "Database": state.appendingPathComponent("state.db").path, "Pin": pin])
            let error = String(decoding: data, as: UTF8.self).withCString { DDFullSyncStart($0) }
            if let error { DDFullSyncFree(error); throw CocoaError(.fileWriteUnknown) }
            running = true
            statusKey = "fullSync.syncing"
            polling = Task { [weak self] in
                while !Task.isCancelled {
                    self?.readStatus()
                    do { try await Task.sleep(for: .seconds(1)) } catch { break }
                }
            }
        } catch { statusKey = "fullSync.error" }
    }

    private func stop() async {
        while busy { await Task.detached { try? await Task.sleep(for: .milliseconds(25)) }.value }
        guard running else { return }
        busy = true
        defer {
            busy = false
            // The scene can become active while the native engine is still stopping.
            if enabled, foreground { Task { await self.start() } }
        }
        polling?.cancel(); polling = nil
        await Task.detached { DDFullSyncStop() }.value
        running = false
        activity = SyncActivity(); activityError = ""
        statusKey = "fullSync.stopped"
    }

    private func backgroundPass() async -> Bool {
        guard enabled, !backgroundGrant else { return false }
        backgroundGrant = true
        schedule()
        await setEnabled(true)
        while !Task.isCancelled, enabled, running,
              statusKey == "fullSync.syncing" || statusKey == "fullSync.preparing" {
            do { try await Task.sleep(for: .seconds(1)) } catch { break }
        }
        let success = !Task.isCancelled && statusKey == "fullSync.ready"
        backgroundGrant = false
        if !foreground { await stop() }
        return success
    }

    func confirmDeletion() {
        guard running, statusKey == "fullSync.bulkDelete" else { return }
        DDFullSyncConfirmDeletion()
        statusKey = "fullSync.syncing"
    }

    private func readStatus() {
        guard running, let raw = DDFullSyncStatus() else { return }
        defer { DDFullSyncFree(raw) }
        guard let json = try? JSONSerialization.jsonObject(with: Data(String(cString: raw).utf8)) as? [String: Any] else { return }
        activityError = json["last_error"] as? String ?? ""
        if let payload = json["activity"], let data = try? JSONSerialization.data(withJSONObject: payload),
           let snapshot = try? JSONDecoder().decode(SyncActivity.self, from: data) { activity = snapshot }
        if json["error_kind"] as? String == "bulk_delete" { statusKey = "fullSync.bulkDelete"; return }
        switch json["state"] as? String {
        case "idle": statusKey = "fullSync.ready"
        case "syncing": statusKey = "fullSync.syncing"
        default: statusKey = "fullSync.error"
        }
    }
}
