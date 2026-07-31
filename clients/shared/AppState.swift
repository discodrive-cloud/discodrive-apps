import Foundation
import SwiftUI
import DiscoKit
#if canImport(AppKit)
import AppKit
#endif
#if canImport(UIKit)
import UIKit
#endif

@MainActor
final class AppState: ObservableObject {
    @Published var paired: Bool = false
    // Sync status for the menu-bar icon (macOS): idle / syncing / offline.
    enum SyncStatus: Sendable { case idle, syncing, offline }
    @Published var syncStatus: SyncStatus = .offline
    @Published var statusText: String = ""
    @Published var revision: Int = 0   // bump → redraw statuses/previews (instead of manual objectWillChange)
    @Published var language: String = "en" {   // UI language (source of truth is the server)
        didSet { UserDefaults.standard.set(language, forKey: "ui_language") }
    }

    // Returns the localized string for a key in the current language.
    func t(_ key: String) -> String { L10n.t(key, language) }

    private(set) var serverURL: URL?
    private(set) var client: APIClient?
    private(set) var index: IndexStore?
    private(set) var local: LocalStore?

    private var refreshing = false
    private var eventsTask: Task<Void, Never>?

    private var appSupportDir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return base.appendingPathComponent("DiscoDrive", isDirectory: true)
    }

    func bootstrap() {
        guard let token = KeychainToken.load(service: KeychainToken.tokenService),
              let urlStr = KeychainToken.load(service: KeychainToken.serverService),
              let url = URL(string: urlStr) else { paired = false; return }
        activate(serverURL: url, token: token)
    }

    func activate(serverURL: URL, token: String) {
        let dir = appSupportDir
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        self.serverURL = serverURL
        self.client = APIClient(baseURL: serverURL, deviceToken: token)
        self.index = try? IndexStore(path: dir.appendingPathComponent("index.sqlite").path)
        #if os(iOS)
        // Content goes directly into Documents (that is the "DiscoDrive" folder visible in Files.app
        // where the user drops files); the internal DB lives in Application Support.
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        self.local = try? LocalStore(dbDirectory: dir.appendingPathComponent("local"),
                                     contentDirectory: docs)
        #else
        self.local = try? LocalStore(directory: dir.appendingPathComponent("local"))
        #endif
        self.paired = (index != nil && local != nil)
        Task { await loadLanguage() }
        startLiveUpdates()
    }

    // Live updates: maintain an SSE connection to /sync/events, refresh on every event.
    // Reconnects on disconnect with backoff. Stopped on logout.
    func startLiveUpdates() {
        guard let client, let serverURL else { return }
        eventsTask?.cancel()
        eventsTask = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                do {
                    let jwt = try await client.authToken()
                    var req = URLRequest(url: serverURL.appendingPathComponent("sync/events"))
                    req.setValue("Bearer \(jwt)", forHTTPHeaderField: "Authorization")
                    req.timeoutInterval = 600
                    let (bytes, resp) = try await DiscoNet.session.bytes(for: req)
                    let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
                    if code == 401 { await client.resetAuth() }
                    else if code == 200 {
                        for try await line in bytes.lines {
                            if Task.isCancelled { break }
                            if line.hasPrefix("data:") { await self?.refresh() }
                        }
                    }
                } catch { /* disconnected — will reconnect below */ }
                if Task.isCancelled { break }
                try? await Task.sleep(for: .seconds(3))   // backoff before reconnecting
            }
        }
    }

    func stopLiveUpdates() { eventsTask?.cancel(); eventsTask = nil }

    // UI language is stored on the server (keeps it in sync across devices).
    func loadLanguage() async {
        if let lang = try? await client?.getLanguage(), L10n.supported.contains(lang) {
            language = lang
        }
    }

    func setLanguage(_ lang: String) async {
        language = lang
        try? await client?.setLanguage(lang)
    }

    // Sign out: disconnect from the server (clear token/URL), return to the pairing screen.
    // Local cached files and the on-disk index are left intact.
    func logout() {
        stopLiveUpdates()
        KeychainToken.delete(service: KeychainToken.tokenService)
        KeychainToken.delete(service: KeychainToken.serverService)
        client = nil; index = nil; local = nil; serverURL = nil
        statusText = ""
        paired = false
        syncStatus = .offline
    }

    // iOS: URL to present in QuickLook (macOS opens files via NSWorkspace).
    @Published var fileToPreview: URL?

    // Open the local folder of downloaded files in Finder (macOS only).
    func openLocalFolderInFinder() {
        #if os(macOS)
        let dir = appSupportDir.appendingPathComponent("local/content", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        NSWorkspace.shared.open(dir)
        #endif
    }

    // Pairing: returns PairingInfo (with verification_uri), then call confirm(deviceCode:).
    func startPairing(serverURL: URL) async throws -> PairingInfo {
        #if os(macOS)
        let deviceName = Host.current().localizedName ?? "Mac"
        #else
        let deviceName = UIDevice.current.name
        #endif
        return try await Pairing(baseURL: serverURL).start(deviceName: deviceName)
    }

    func confirmPairing(serverURL: URL, info: PairingInfo) async throws {
        let token = try await Pairing(baseURL: serverURL)
            .poll(deviceCode: info.deviceCode, interval: .seconds(max(1, info.interval)))
        // A new pairing starts from a clean slate: forget any previously-paired server's
        // index and downloaded files, so old content is never shown or pushed to the new server.
        resetLocalState()
        KeychainToken.save(token, service: KeychainToken.tokenService)
        KeychainToken.save(serverURL.absoluteString, service: KeychainToken.serverService)
        activate(serverURL: serverURL, token: token)
    }

    // Wipe all locally-cached state from a previous pairing: the file-tree index and the
    // downloaded-files store (cache DB + content). Called on every pairing. Downloads are
    // on-demand, so the only cost is re-downloading opened/pinned files from the new server.
    private func resetLocalState() {
        stopLiveUpdates()
        index = nil          // release the SQLite handles before deleting the files
        local = nil
        let fm = FileManager.default
        let dir = appSupportDir
        for name in ["index.sqlite", "index.sqlite-wal", "index.sqlite-shm"] {
            try? fm.removeItem(at: dir.appendingPathComponent(name))
        }
        try? fm.removeItem(at: dir.appendingPathComponent("local"))   // macOS: cache DB + content
        #if os(iOS)
        // iOS content lives in the app's Documents (the user-visible "DiscoDrive" folder) — clear it too.
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        for item in (try? fm.contentsOfDirectory(at: docs, includingPropertiesForKeys: nil)) ?? [] {
            try? fm.removeItem(at: item)
        }
        #endif
    }

    func refresh() async {
        guard let client, let index, !refreshing else { return }
        refreshing = true; defer { refreshing = false }
        syncStatus = .syncing
        do {
            var cursor = try index.cursor()
            while true {
                let page = try await client.changes(since: cursor, limit: 500)
                try index.apply(page.changes)
                cursor = page.cursor
                if !page.hasMore { break }
            }
            try index.setCursor(cursor)
            statusText = t("status.updated")
            syncStatus = .idle
        } catch {
            statusText = "\(t("status.refreshError")): \(error.localizedDescription)"
            syncStatus = .offline
        }
    }

    // MARK: - Task 12: browser helpers

    func children(of parentID: String?) -> [Node] {
        (try? index?.children(of: parentID)) ?? []
    }

    struct FolderItem: Identifiable {
        let node: Node
        var children: [FolderItem]?
        var id: String { node.id }
    }

    func folderTree() -> [FolderItem] {
        func build(_ parentID: String?) -> [FolderItem] {
            children(of: parentID).filter(\.isDir).map { dir in
                FolderItem(node: dir, children: build(dir.id).isEmpty ? nil : build(dir.id))
            }
        }
        return build(nil)
    }

    func status(of node: Node) -> LocalStatus {
        guard !node.isDir, let local else { return .none }
        return (try? local.status(nodeID: node.id, serverVersion: node.version)) ?? .none
    }

    // IDs of files currently being downloaded (drives the spinner in the row).
    @Published var downloadingIDs: Set<String> = []
    func isDownloading(_ node: Node) -> Bool { downloadingIDs.contains(node.id) }

    func node(id: String) -> Node? { try? index?.node(id: id) }

    // MARK: - Write Operations

    func upload(_ urls: [URL], toFolderPath folderPath: String) async {
        guard let client else { return }
        for url in urls {
            let rel = folderPath + "/" + url.lastPathComponent
            // Streamed from disk: a big file no longer has to fit in memory, and a read
            // failure throws here instead of silently skipping the file the way the old
            // `try? Data(contentsOf:)` did — that left the user thinking it had uploaded.
            do {
                try await client.uploadFile(relPath: rel, fileURL: url,
                                            modifiedAt: APIClient.contentModificationDate(of: url))
            } catch { statusText = "\(t("status.uploadError")): \(error.localizedDescription)" }
        }
        await refresh()
    }

    func createFolder(name: String, inFolderPath folderPath: String) async {
        guard let client, !name.isEmpty else { return }
        try? await client.createDir(relPath: folderPath + "/" + name)
        await refresh()
    }

    func deleteNode(_ node: Node) async {
        guard let client else { return }
        try? await client.delete(nodeID: node.id)
        try? local?.remove(nodeID: node.id)
        await refresh()
    }

    func renameNode(_ node: Node, to newName: String) async {
        guard let client, !newName.isEmpty, newName != node.name else { return }
        try? await client.rename(nodeID: node.id, newName: newName)
        await refresh()
    }

    // MARK: - Vault (Cryptomator E2E-vault)

    struct VaultSession {
        let vault: Vault
        let io: ServerVaultIO
        let name: String
    }
    @Published var vaultUnlockFolder: Node?       // != nil → show password prompt
    @Published var vaultUnlockError: String?
    @Published var vaultUnlocking = false
    @Published var vaultSession: VaultSession?     // != nil → show vault browser
    @Published var vaultRecoveryToShow: String?    // != nil → show new vault's recovery key

    // A folder is a Cryptomator vault if it contains masterkey.cryptomator + vault.cryptomator.
    func isVault(_ folder: Node) -> Bool {
        guard folder.isDir, let index else { return false }
        let names = Set(((try? index.children(of: folder.id)) ?? []).map(\.name))
        return names.contains("masterkey.cryptomator") && names.contains("vault.cryptomator")
    }

    func openVault(_ folder: Node, password: String, remember: Bool = false) async {
        guard !vaultUnlocking else { return }
        vaultUnlocking = true; defer { vaultUnlocking = false }
        await unlock(folder) { io in try await Vault.open(source: io, password: password) }
        if remember, vaultSession != nil {
            VaultPasswordStore.save(password: password, forVault: folder.path)
        }
    }

    func openVaultWithRecovery(_ folder: Node, phrase: String) async {
        guard !vaultUnlocking else { return }
        vaultUnlocking = true; defer { vaultUnlocking = false }
        await unlock(folder) { io in try await Vault.open(source: io, recoveryPhrase: phrase) }
    }

    // Biometrics.
    var vaultBiometry: BiometryKind { VaultPasswordStore.biometry() }
    func vaultHasSavedPassword(_ folder: Node) -> Bool { VaultPasswordStore.hasPassword(forVault: folder.path) }
    func forgetVaultPassword(_ folder: Node) { VaultPasswordStore.delete(forVault: folder.path) }

    // Unlock a vault using Face ID / Touch ID (password retrieved from Keychain).
    func openVaultBiometric(_ folder: Node) async {
        guard !vaultUnlocking else { return }
        vaultUnlocking = true; defer { vaultUnlocking = false }
        vaultUnlockError = nil
        do {
            let pw = try await VaultPasswordStore.loadPassword(forVault: folder.path, reason: t("vault.unlockReason"))
            await unlock(folder) { io in try await Vault.open(source: io, password: pw) }
        } catch VaultPasswordStore.VaultPWError.cancelled {
            // user cancelled — silently do nothing
        } catch {
            vaultUnlockError = error.localizedDescription
        }
    }

    // Shared core: open a vault with the provided opener. Callers must set vaultUnlocking.
    private func unlock(_ folder: Node, _ opener: (ServerVaultIO) async throws -> Vault) async {
        guard let index, let client else { return }
        vaultUnlockError = nil
        let io = ServerVaultIO(vaultRoot: folder.path, index: index, client: client)
        do {
            let vault = try await opener(io)
            vaultSession = VaultSession(vault: vault, io: io, name: folder.name)
            vaultUnlockFolder = nil
        } catch {
            vaultUnlockError = (error as? Vault.VaultError) == .wrongPassword
                ? t("vault.wrongPassword") : error.localizedDescription
        }
    }

    func closeVault() { vaultSession = nil }

    func createVault(name: String, inFolderPath: String, password: String) async {
        guard let index, let client, !name.isEmpty, !password.isEmpty else { return }
        let vaultPath = inFolderPath + "/" + name
        try? await client.createDir(relPath: vaultPath)
        let io = ServerVaultIO(vaultRoot: vaultPath, index: index, client: client)
        do {
            let vault = try await Vault.create(sink: io, password: password)
            vaultRecoveryToShow = vault.recoveryKey()
            await refresh()
        } catch { statusText = "\(t("vault.createError")): \(error.localizedDescription)" }
    }

    // MARK: - Task 13: download / open / pin

    // Ensures a fresh local copy is available (downloads if missing or stale). Returns the URL.
    @discardableResult
    func ensureDownloaded(_ node: Node, pin: Bool = false) async -> URL? {
        guard let client, let local else { return nil }
        let st = (try? local.status(nodeID: node.id, serverVersion: node.version)) ?? .none
        let needsDownload = (st == .none || st == .stale)
        do {
            if needsDownload {
                downloadingIDs.insert(node.id)
                defer { downloadingIDs.remove(node.id) }
                let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
                try await client.download(nodeID: node.id, to: tmp)
                try local.store(nodeID: node.id, version: node.version, from: tmp, pinned: pin, relPath: node.path)
            } else if pin {
                try local.pin(nodeID: node.id)
            }
            revision += 1
            return local.localURL(nodeID: node.id)
        } catch {
            statusText = "\(t("status.downloadError")): \(error.localizedDescription)"
            return nil
        }
    }

    func openFile(_ node: Node) async {
        guard !node.isDir, let url = await ensureDownloaded(node) else { return }
        #if os(macOS)
        NSWorkspace.shared.open(url)
        #else
        fileToPreview = url
        #endif
    }

    func pin(_ node: Node) async {
        await ensureDownloaded(node, pin: true)
    }

    // Remove the local copy and redraw the row status (otherwise the checkmark lingers until refresh).
    func removeLocal(_ node: Node) {
        try? local?.remove(nodeID: node.id)
        revision += 1
    }

    @Published var importing = false

    // Import: files the user placed in the local folder themselves (not yet in the index)
    // are uploaded to the server. After a refresh they appear in the index and won't be re-uploaded.
    func importLocalFiles() async {
        guard let local, let index, let client, !importing else { return }
        importing = true; defer { importing = false }
        let fm = FileManager.default
        // Resolve symlinks: the enumerator returns /private/var/…, but contentDirectory is /var/… (a symlink).
        let basePath = local.contentDirectory.resolvingSymlinksInPath().path
        guard let en = fm.enumerator(at: local.contentDirectory, includingPropertiesForKeys: [.isRegularFileKey],
                                     options: [.skipsHiddenFiles]) else { return }
        var newFiles: [(url: URL, rel: String)] = []
        for url in en.allObjects.compactMap({ $0 as? URL }) {
            var isDir: ObjCBool = false
            _ = fm.fileExists(atPath: url.path, isDirectory: &isDir)
            if isDir.boolValue { continue }
            let path = url.resolvingSymlinksInPath().path
            guard path.hasPrefix(basePath) else { continue }
            var rel = String(path.dropFirst(basePath.count))
            if !rel.hasPrefix("/") { rel = "/" + rel }
            if ((try? index.node(atPath: rel)) ?? nil) != nil { continue }   // already on server
            newFiles.append((url, rel))
        }
        guard !newFiles.isEmpty else { return }
        for f in newFiles {
            do {
                try await client.uploadFile(relPath: f.rel, fileURL: f.url,
                                            modifiedAt: APIClient.contentModificationDate(of: f.url))
            } catch { statusText = "\(t("status.uploadError")): \(error.localizedDescription)" }
        }
        await refresh()
        // Register uploaded files as local copies (show them as cached, skip re-downloading).
        for f in newFiles {
            if let node = try? index.node(atPath: f.rel) {
                try? local.registerExisting(nodeID: node.id, version: node.version, relPath: f.rel)
            }
        }
        revision += 1
    }
}
