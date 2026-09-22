import AppKit
import CryptoKit
import DiscoKit
import Foundation

/// Owns the independent, fully materialized mirror. Finder's download cache is never used.
@MainActor
final class FullSyncController: ObservableObject {
    @Published private(set) var enabled = UserDefaults.standard.bool(forKey: "fullSync.enabled")
    @Published private(set) var folder: URL?
    @Published private(set) var busy = false
    @Published private(set) var statusKey = "fullSync.stopped"
    @Published private(set) var backup: URL?
    private var running = false
    private var quitting = false
    private var access: URL?
    private var timer: Timer?
    private weak var app: AppState?
    private let defaults = UserDefaults.standard

    init() {
        if let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first {
            let saved = support.appendingPathComponent("FullSync/Backups")
            if FileManager.default.fileExists(atPath: saved.path) { backup = saved }
        }
        guard let data = defaults.data(forKey: "fullSync.bookmark") else { return }
        do {
            var stale = false
            let url = try URL(resolvingBookmarkData: data, options: [.withSecurityScope, .withoutUI],
                              relativeTo: nil, bookmarkDataIsStale: &stale)
            folder = url
            if stale {
                guard url.startAccessingSecurityScopedResource() else { throw CocoaError(.fileReadNoPermission) }
                defer { url.stopAccessingSecurityScopedResource() }
                try saveBookmark(url)
            }
        } catch { statusKey = "fullSync.accessError" }
    }

    func attach(_ app: AppState) {
        self.app = app
        if enabled, !running, !busy, app.paired { setEnabled(true) }
    }

    private func saveBookmark(_ url: URL) throws {
        let data = try url.bookmarkData(options: .withSecurityScope, includingResourceValuesForKeys: nil, relativeTo: nil)
        defaults.set(data, forKey: "fullSync.bookmark")
    }

    func chooseFolder() {
        guard !busy, !enabled else { return }
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.message = app?.t("fullSync.chooseHint") ?? ""
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try validate(url)
            try saveBookmark(url)
            folder = url
            statusKey = "fullSync.stopped"
        } catch { statusKey = "fullSync.folderError" }
    }

    private func validate(_ url: URL) throws {
        let path = url.resolvingSymlinksInPath().standardizedFileURL.path
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let realHome = URL(fileURLWithPath: NSHomeDirectory()).path
        // A sync root must be a dedicated folder, never a parent of application state
        // or another provider's managed storage.
        let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                                  appropriateFor: nil, create: true).path
        guard path != "/", path != home, path != realHome,
              !support.hasPrefix(path + "/"), !path.hasPrefix(support + "/"), path != support,
              !path.contains("/Library/CloudStorage/"),
              !path.hasSuffix("/Library/CloudStorage"),
              !(try url.resourceValues(forKeys: [.isVolumeKey])).isVolume.orFalse else {
            throw CocoaError(.fileWriteNoPermission)
        }
    }

    func setEnabled(_ value: Bool) {
        guard !busy else { return }
        if value, folder == nil { statusKey = "fullSync.accessError"; return }
        enabled = value
        defaults.set(value, forKey: "fullSync.enabled")
        busy = true
        Task {
            await stop()
            if value, app?.paired == true { await start() }
            busy = false
        }
    }

    /// Logging out disables the old account's mirror, even if a new pairing follows.
    func logout() async {
        enabled = false
        defaults.set(false, forKey: "fullSync.enabled")
        // A start may be preparing a large backup off the main actor.
        while busy { try? await Task.sleep(for: .milliseconds(50)) }
        await stop()
    }

    func quit() async {
        quitting = true
        while busy { try? await Task.sleep(for: .milliseconds(50)) }
        await stop()
    }

    func stop() async {
        timer?.invalidate(); timer = nil
        statusKey = "fullSync.stopped"
        guard running else { return }
        await Task.detached { DDFullSyncStop() }.value
        running = false
        access?.stopAccessingSecurityScopedResource(); access = nil
        statusKey = "fullSync.stopped"
    }

    private func start() async {
        guard !quitting, enabled, let app, app.paired, let server = app.serverURL, let folder else { return }
        guard folder.startAccessingSecurityScopedResource() else { statusKey = "fullSync.accessError"; return }
        access = folder
        do {
            try validate(folder)
            guard let token = try KeychainToken.loadShared(service: KeychainToken.tokenService) else {
                throw CocoaError(.fileReadNoPermission)
            }
            let attributes = try FileManager.default.attributesOfItem(atPath: folder.path)
            // A replacement directory or another account must never inherit deletion history.
            guard let volume = attributes[.systemNumber], let inode = attributes[.systemFileNumber] else {
                throw CocoaError(.fileReadUnknown)
            }
            let identity = "\(server.absoluteString)\n\(token)\n\(volume)\n\(inode)"
            let key = SHA256.hash(data: Data(identity.utf8)).map { String(format: "%02x", $0) }.joined()
            let support = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                                      appropriateFor: nil, create: true).appendingPathComponent("FullSync")
            let state = support.appendingPathComponent(key)
            let database = state.appendingPathComponent("state.db")
            try FileManager.default.createDirectory(at: state, withIntermediateDirectories: true)
            statusKey = "fullSync.preparing"
            let backups = support.appendingPathComponent("Backups")
            backup = backups
            _ = try await Task.detached {
                try FullSyncStorage.prepare(folder: folder, state: state, backups: support.appendingPathComponent("Backups"))
            }.value
            if !FileManager.default.fileExists(atPath: backups.path) { backup = nil }
            guard !quitting, enabled, app.paired, app.serverURL == server else {
                access?.stopAccessingSecurityScopedResource(); access = nil
                statusKey = "fullSync.stopped"
                return
            }
            let config = try JSONSerialization.data(withJSONObject: [
                "Server": server.absoluteString, "Token": token, "Root": folder.path, "Database": database.path,
            ])
            let message = String(decoding: config, as: UTF8.self).withCString { DDFullSyncStart($0) }
            if let message { DDFullSyncFree(message); throw CocoaError(.fileWriteUnknown) }
            running = true
            statusKey = "fullSync.syncing"
            timer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
                Task { @MainActor in self?.readStatus() }
            }
        } catch {
            statusKey = "fullSync.error"
            access?.stopAccessingSecurityScopedResource(); access = nil
        }
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
        if json["error_kind"] as? String == "bulk_delete" {
            statusKey = "fullSync.bulkDelete"
            return
        }
        switch json["state"] as? String {
        case "idle": statusKey = "fullSync.ready"
        case "syncing": statusKey = "fullSync.syncing"
        default: statusKey = "fullSync.error"
        }
    }
}

private extension Optional where Wrapped == Bool {
    var orFalse: Bool { self ?? false }
}
