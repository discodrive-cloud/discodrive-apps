import Foundation
import SwiftUI
import DiscoKit
import os
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
    // The last failure the user should see; the browser shows it as a banner until
    // dismissed. Set wherever an operation fails, cleared by the user.
    @Published var lastError: String?
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

    // The folder tree and the set of vault folders, rebuilt from the index after every
    // refresh — off the main thread, in one pass over the nodes. The views read these;
    // they never query the database while drawing, which is what froze the window for
    // seconds on every click (a query per folder, recursively, per redraw).
    @Published private(set) var tree: [FolderItem] = []
    @Published private(set) var vaultIDs: Set<String> = []

    private var session = AccountSession()
    @Published private(set) var loggingOut = false
    private var refreshing = false
    private var eventsTask: Task<Void, Never>?
    static let log = Logger(subsystem: "org.discodrive.app", category: "state")
    // Called after every successful refresh; the macOS app uses it to nudge the File
    // Provider extension so Finder picks up the change without waiting.
    var onRemoteChange: (() -> Void)?

    // Set by the macOS app at launch; nil on iOS. With a group the index lives in the group
    // container, where the File Provider extension can open it too.
    nonisolated(unsafe) static var appGroupID: String?

    private var appSupportDir: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return base.appendingPathComponent("DiscoDrive", isDirectory: true)
    }

    // Where the index database goes: the App Group container when there is one (shared with
    // the extension), the app's own Application Support otherwise.
    private var indexDir: URL {
        if let group = Self.appGroupID,
           let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group) {
            return container.appendingPathComponent("DiscoDrive", isDirectory: true)
        }
        return appSupportDir
    }

    // Runs once, from init, so the first frame already knows whether the device is paired;
    // calling it again later is harmless.
    init() { bootstrap() }

    func bootstrap() {
        #if DEBUG
        // DISCODRIVE_TEST_REPAIR=1 drops the current pairing first, so a test run can move
        // the app to the test account without driving the log-out button.
        if paired, ProcessInfo.processInfo.environment["DISCODRIVE_TEST_REPAIR"] == "1",
           ProcessInfo.processInfo.environment["DISCODRIVE_TEST_TOKEN"] != nil { finishLogout() }   // at once: the pairing below follows
        #endif
        guard !paired else { return }
        #if DEBUG
        // Lets an automated run pair the app without driving the pairing screen by hand:
        // the simulator has no way to type a device code, and the E2E checks need a real
        // paired app against a local server. Debug builds only.
        let env = ProcessInfo.processInfo.environment
        if let urlStr = env["DISCODRIVE_TEST_SERVER"], let token = env["DISCODRIVE_TEST_TOKEN"],
           let url = URL(string: urlStr) {
            KeychainToken.save(token, service: KeychainToken.tokenService)
            KeychainToken.save(urlStr, service: KeychainToken.serverService)
            activate(serverURL: url, token: token)
            return
        }
        #endif
        guard let token = KeychainToken.load(service: KeychainToken.tokenService),
              let urlStr = KeychainToken.load(service: KeychainToken.serverService),
              let url = URL(string: urlStr) else { paired = false; return }
        activate(serverURL: url, token: token)
    }

    func activate(serverURL: URL, token: String) {
        session.invalidate()
        session = AccountSession()
        refreshing = false; importing = false; downloadingIDs = []
        let dir = appSupportDir
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try? FileManager.default.createDirectory(at: indexDir, withIntermediateDirectories: true)
        self.serverURL = serverURL
        self.client = APIClient(baseURL: serverURL, deviceToken: token)
        self.index = try? IndexStore(path: indexDir.appendingPathComponent("index.sqlite").path)
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
        Task { await rebuildTree() }
        Self.log.notice("activated against \(serverURL.absoluteString, privacy: .public), index at \(self.indexDir.path, privacy: .public) (\(self.index == nil ? "FAILED" : "ok", privacy: .public)), paired=\(self.paired)")
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
        guard let client, session.isActive else { return }
        let session = self.session
        if let lang = try? await session.perform({ try await client.getLanguage() }),
           session.isActive, L10n.supported.contains(lang) { language = lang }
    }

    func setLanguage(_ lang: String) async {
        guard let client, session.isActive else { return }
        let session = self.session
        language = lang
        try? await session.perform { try await client.setLanguage(lang) }
    }

    // Stop account work and disconnect Finder before clearing credentials and local state.
    //
    // What must be over before the account's state goes — on macOS, closing the vaults open
    // in Finder: their domains, keys and decrypted files belong to the account being left.
    // False means it is not over; the logout then does not happen, because what it would
    // leave behind — an extension still holding the keys and a client — is the very
    // thing logging out is for.
    var beforeLogout: (() async -> Bool)?

    func logout() {
        guard paired, !loggingOut else { return }
        loggingOut = true
        let leaving = session
        leaving.invalidate()
        stopLiveUpdates()
        vaultSession = nil
        vaultUnlockFolder = nil; vaultUnlocking = false; vaultRecoveryToShow = nil
        Task {
            await leaving.stop()
            if let beforeLogout, await !beforeLogout() {
                Self.log.error("logout held back: File Provider domains could not be closed")
                session = AccountSession()
                refreshing = false; importing = false; downloadingIDs = []
                loggingOut = false
                statusText = t("logout.domainsStillOpen"); lastError = statusText
                startLiveUpdates()
                return
            }
            finishLogout()
            loggingOut = false
        }
    }

    private func finishLogout() {
        session.invalidate()
        stopLiveUpdates()
        KeychainToken.delete(service: KeychainToken.tokenService)
        KeychainToken.delete(service: KeychainToken.serverService)
        client = nil; index = nil; local = nil; serverURL = nil
        statusText = ""
        tree = []; vaultIDs = []; fileToPreview = nil; downloadingIDs = []
        paired = false
        syncStatus = .offline
        forgetLocalState()
    }

    // After a pairing ends the server is the only truth: the index and the download
    // bookkeeping go, and the downloaded files are set aside under a dated name rather
    // than kept where the next pairing would mistake them for its own.
    private func forgetLocalState() {
        let fm = FileManager.default
        for suffix in ["", "-wal", "-shm"] {
            try? fm.removeItem(at: indexDir.appendingPathComponent("index.sqlite" + suffix))
        }
        let local = appSupportDir.appendingPathComponent("local", isDirectory: true)
        try? fm.removeItem(at: local.appendingPathComponent("local.sqlite"))
        let content = local.appendingPathComponent("content", isDirectory: true)
        if fm.fileExists(atPath: content.path) {
            let stamp = ISO8601DateFormatter().string(from: Date()).replacingOccurrences(of: ":", with: "-")
            try? fm.moveItem(at: content, to: local.appendingPathComponent("content.old-" + stamp, isDirectory: true))
        }
        Self.log.notice("local state forgotten after logout")
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
        await session.stop()
        resetLocalState()
        KeychainToken.save(token, service: KeychainToken.tokenService)
        KeychainToken.save(serverURL.absoluteString, service: KeychainToken.serverService)
        activate(serverURL: serverURL, token: token)
    }

    // Wipe all locally-cached state from a previous pairing: the file-tree index and the
    // downloaded-files store (cache DB + content). Called on every pairing. Downloads are
    // on-demand, so the only cost is re-downloading opened/pinned files from the new server.
    private func resetLocalState() {
        session.invalidate()
        stopLiveUpdates()
        index = nil          // release the SQLite handles before deleting the files
        local = nil
        let fm = FileManager.default
        for name in ["index.sqlite", "index.sqlite-wal", "index.sqlite-shm"] {
            try? fm.removeItem(at: indexDir.appendingPathComponent(name))
        }
        try? fm.removeItem(at: appSupportDir.appendingPathComponent("local"))   // macOS: cache DB + content
        #if os(iOS)
        // iOS content lives in the app's Documents (the user-visible "DiscoDrive" folder) — clear it too.
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        for item in (try? fm.contentsOfDirectory(at: docs, includingPropertiesForKeys: nil)) ?? [] {
            try? fm.removeItem(at: item)
        }
        // Auto-upload's destination is a node id on the previous server; forget it with
        // the rest, or the first pass after pairing would aim at a folder that is not there.
        AutoUploadSettings.shared.reset()
        #endif
    }

    // True when the index now holds what the server said; false when the pull failed or
    // another one was already running.
    @discardableResult
    func refresh() async -> Bool {
        guard let client, let index, session.isActive, !refreshing else {
            Self.log.notice("refresh skipped (client=\(self.client == nil ? 0 : 1) index=\(self.index == nil ? 0 : 1) refreshing=\(self.refreshing))")
            return false
        }
        let session = self.session
        refreshing = true; defer { if session.isActive { refreshing = false } }
        syncStatus = .syncing
        do {
            // The pull — network plus applying pages to SQLite — runs away from the main
            // actor; the first one after pairing applies the whole tree and used to hold
            // the window for its duration.
            let cursor = try await session.perform { () throws -> Int64 in
                var cursor = try index.cursor()
                while true {
                    let page = try await client.changes(since: cursor, limit: 500)
                    try Task.checkCancellation()
                    try index.apply(page.changes)
                    cursor = page.cursor
                    if !page.hasMore { break }
                }
                try index.setCursor(cursor)
                return cursor
            }
            await rebuildTree()
            try session.check()
            statusText = t("status.updated")
            syncStatus = .idle
            Self.log.notice("refreshed: \(self.tree.count) root folders, cursor \(cursor)")
            onRemoteChange?()
            return true
        } catch {
            guard session.isActive, !(error is CancellationError) else { return false }
            // The full error goes to the log; the window gets a plain reason only when
            // there is one worth reading. A hiccup in the shared index or a dropped
            // connection is retried on the next refresh without a word.
            Self.log.error("refresh failed: \(String(describing: error), privacy: .public)")
            switch Self.kind(of: error) {
            case .sessionExpired:
                statusText = t("status.sessionExpired"); lastError = statusText; syncStatus = .offline
            case .offline, .serverError:
                statusText = t("status.offline"); syncStatus = .offline
            case .rejected, .unexplained:
                syncStatus = .idle
            }
            return false
        }
    }

    enum FailureKind { case sessionExpired, offline, serverError, rejected, unexplained }

    // What a failure means to the person at the window, if anything.
    static func kind(of error: Error) -> FailureKind {
        switch error {
        case APIError.notAuthenticated, APIError.http(401), APIError.http(403): return .sessionExpired
        case APIError.http(let code) where code >= 500: return .serverError
        case APIError.http: return .rejected
        case is URLError: return .offline
        default: return .unexplained
        }
    }

    // A plain-language reason for the banner, or nil when only the log can explain it.
    func userMessage(for error: Error) -> String? {
        switch Self.kind(of: error) {
        case .sessionExpired: return t("status.sessionExpired")
        case .offline: return t("status.offline")
        case .serverError: return t("status.serverError")
        case .rejected:
            if case APIError.http(let code) = error { return "\(t("status.rejected")) (\(code))" }
            return t("status.rejected")
        case .unexplained: return nil
        }
    }

    // MARK: - Task 12: browser helpers

    func children(of parentID: String?) -> [Node] {
        (try? index?.children(of: parentID)) ?? []
    }

    struct FolderItem: Identifiable, Sendable {
        let node: Node
        var children: [FolderItem]?
        var id: String { node.id }
    }

    // The cached folder tree (see `tree`); kept for callers that used to build it here.
    func folderTree() -> [FolderItem] { tree }

    // One pass over the index, off the main thread: every folder's children, and which
    // folders are Cryptomator vaults (they hold masterkey.cryptomator + vault.cryptomator).
    func rebuildTree() async {
        let session = self.session
        guard session.isActive else { return }
        guard let index else { tree = []; vaultIDs = []; return }
        guard let built = try? await session.perform({ () -> ([FolderItem], Set<String>) in
            guard let nodes = try? index.allNodes() else { return ([], []) }
            var kids: [String?: [Node]] = [:]
            var names: [String: Set<String>] = [:]
            for n in nodes {
                if n.isDir { kids[n.parentID, default: []].append(n) }
                if let p = n.parentID, !n.isDir { names[p, default: []].insert(n.name) }
            }
            let vaults = Set(names.filter { $0.value.contains("masterkey.cryptomator") && $0.value.contains("vault.cryptomator") }.keys)
            func build(_ parent: String?) -> [FolderItem] {
                (kids[parent] ?? []).sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }.map { dir in
                    let sub = build(dir.id)
                    return FolderItem(node: dir, children: sub.isEmpty ? nil : sub)
                }
            }
            return (build(nil), vaults)
        }) else { return }
        guard session.isActive else { return }
        tree = built.0
        vaultIDs = built.1
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
        guard let client, session.isActive else { return }
        let session = self.session
        for url in urls {
            guard session.isActive else { return }
            let rel = folderPath + "/" + url.lastPathComponent
            do {
                _ = try await session.perform {
                    try await client.upload(fileURL: url, relPath: rel,
                                            modifiedAt: APIClient.contentModificationDate(of: url))
                }
            } catch { if session.isActive { fail("status.uploadError", error) } }
        }
        if session.isActive { await refresh() }
    }

    // Reports a failed operation the user asked for: the banner names what failed and,
    // when it can be said plainly, why; the log keeps the actual error.
    private func fail(_ key: String, _ error: Error) {
        guard session.isActive, !(error is CancellationError) else { return }
        Self.log.error("\(key, privacy: .public) failed: \(String(describing: error), privacy: .public)")
        statusText = userMessage(for: error).map { "\(t(key)): \($0)" } ?? t(key)
        lastError = statusText
    }

    func createFolder(name: String, inFolderPath folderPath: String) async {
        guard let client, session.isActive, !name.isEmpty else { return }
        let session = self.session
        do { try await session.perform { try await client.createDir(relPath: folderPath + "/" + name) } }
        catch { if session.isActive { fail("status.opError", error) } }
        if session.isActive { await refresh() }
    }

    func deleteNode(_ node: Node) async {
        guard let client, let local, session.isActive else { return }
        let session = self.session
        do {
            try await session.perform { try await client.delete(nodeID: node.id) }
            try session.check()
            try local.remove(nodeID: node.id)
        } catch { if session.isActive { fail("status.opError", error) } }
        if session.isActive { await refresh() }
    }

    func renameNode(_ node: Node, to newName: String) async {
        guard let client, session.isActive, !newName.isEmpty, newName != node.name else { return }
        let session = self.session
        do { try await session.perform { try await client.rename(nodeID: node.id, newName: newName) } }
        catch { if session.isActive { fail("status.opError", error) } }
        if session.isActive { await refresh() }
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
    // When set (macOS), an unlocked vault is handed here — it becomes a Finder location —
    // instead of opening the in-app browser. Returns whether that worked.
    var presentUnlockedVault: ((Vault, Node) async -> Bool)?

    // A folder is a Cryptomator vault if it contains masterkey.cryptomator + vault.cryptomator;
    // answered from the tree built at the last refresh, not from the database.
    func isVault(_ folder: Node) -> Bool { folder.isDir && vaultIDs.contains(folder.id) }

    func openVault(_ folder: Node, password: String, remember: Bool = false) async {
        guard session.isActive, !vaultUnlocking else { return }
        let session = self.session
        vaultUnlocking = true; defer { if session.isActive { vaultUnlocking = false } }
        let opened = await unlock(folder) { io in try await Vault.open(source: io, password: password) }
        if session.isActive, remember, opened {
            VaultPasswordStore.save(password: password, forVault: folder.path)
        }
    }

    func openVaultWithRecovery(_ folder: Node, phrase: String) async {
        guard session.isActive, !vaultUnlocking else { return }
        let session = self.session
        vaultUnlocking = true; defer { if session.isActive { vaultUnlocking = false } }
        _ = await unlock(folder) { io in try await Vault.open(source: io, recoveryPhrase: phrase) }
    }

    // Biometrics.
    var vaultBiometry: BiometryKind { VaultPasswordStore.biometry() }
    func vaultHasSavedPassword(_ folder: Node) -> Bool { VaultPasswordStore.hasPassword(forVault: folder.path) }
    func forgetVaultPassword(_ folder: Node) { VaultPasswordStore.delete(forVault: folder.path) }

    // Unlock a vault using Face ID / Touch ID (password retrieved from Keychain).
    func openVaultBiometric(_ folder: Node) async {
        guard session.isActive, !vaultUnlocking else { return }
        let session = self.session
        vaultUnlocking = true; defer { if session.isActive { vaultUnlocking = false } }
        vaultUnlockError = nil
        do {
            let pw = try await VaultPasswordStore.loadPassword(forVault: folder.path, reason: t("vault.unlockReason"))
            guard session.isActive else { return }
            _ = await unlock(folder) { io in try await Vault.open(source: io, password: pw) }
        } catch VaultPasswordStore.VaultPWError.cancelled {
            // user cancelled — silently do nothing
        } catch {
            guard session.isActive else { return }
            vaultUnlockError = error.localizedDescription
        }
    }

    // Shared core: open a vault with the provided opener. Callers must set vaultUnlocking.
    // True once the vault is open, either as a session here or wherever the platform
    // presents it.
    @discardableResult
    private func unlock(_ folder: Node, _ opener: @escaping @Sendable (ServerVaultIO) async throws -> Vault) async -> Bool {
        guard let index, let client, session.isActive else { return false }
        let session = self.session
        vaultUnlockError = nil
        let io = ServerVaultIO(vaultRoot: folder.path, index: index, client: client)
        do {
            let vault = try await session.perform { try await opener(io) }
            try session.check()
            if let present = presentUnlockedVault {
                let opened = await present(vault, folder)
                guard session.isActive else { return false }
                if opened { vaultUnlockFolder = nil; return true }
                vaultUnlockError = t("status.opError")
                return false
            }
            vaultSession = VaultSession(vault: vault, io: io, name: folder.name)
            vaultUnlockFolder = nil
            return true
        } catch {
            guard session.isActive, !(error is CancellationError) else { return false }
            vaultUnlockError = (error as? Vault.VaultError) == .wrongPassword
                ? t("vault.wrongPassword") : error.localizedDescription
            return false
        }
    }

    func closeVault() { vaultSession = nil }

    func createVault(name: String, inFolderPath: String, password: String) async {
        guard let index, let client, session.isActive, !name.isEmpty, !password.isEmpty else { return }
        let session = self.session
        let vaultPath = inFolderPath + "/" + name
        // A vault is new keys: written into a folder that already is one, they replace its
        // masterkey and everything in it stops opening. The name must be free — by an
        // index that is current — and the files go up as "create only if absent", so a
        // name taken in the meantime fails the creation instead of being written over.
        let refreshed = await refresh()
        guard session.isActive else { return }
        guard refreshed else { statusText = t("vault.createError"); lastError = statusText; return }
        guard ((try? index.node(atPath: vaultPath)) ?? nil) == nil else {
            statusText = "\(t("vault.createError")): \(t("vault.nameTaken"))"
            lastError = statusText
            return
        }
        do {
            let vault = try await session.perform {
                try await client.createDir(relPath: vaultPath)
                try Task.checkCancellation()
                let io = ServerVaultIO(vaultRoot: vaultPath, index: index, client: client, createOnly: true)
                return try await Vault.create(sink: io, password: password)
            }
            try session.check()
            vaultRecoveryToShow = vault.recoveryKey()
            await refresh()
        } catch { if session.isActive { fail("vault.createError", error) } }
    }

    // MARK: - Task 13: download / open / pin

    // Ensures a fresh local copy is available (downloads if missing or stale). Returns the URL.
    @discardableResult
    func ensureDownloaded(_ node: Node, pin: Bool = false) async -> URL? {
        guard let client, let local, session.isActive else { return nil }
        let session = self.session
        let st = (try? local.status(nodeID: node.id, serverVersion: node.version)) ?? .none
        let needsDownload = (st == .none || st == .stale)
        do {
            if needsDownload {
                downloadingIDs.insert(node.id)
                defer { if session.isActive { downloadingIDs.remove(node.id) } }
                let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
                defer { try? FileManager.default.removeItem(at: tmp) }
                try await session.perform { try await client.download(nodeID: node.id, to: tmp) }
                try session.check()
                try local.store(nodeID: node.id, version: node.version, from: tmp, pinned: pin, relPath: node.path)
            } else if pin {
                try local.pin(nodeID: node.id)
            }
            revision += 1
            return local.localURL(nodeID: node.id)
        } catch {
            guard session.isActive, !(error is CancellationError) else { return nil }
            fail("status.downloadError", error)
            return nil
        }
    }

    func openFile(_ node: Node) async {
        let session = self.session
        guard !node.isDir, let url = await ensureDownloaded(node), session.isActive else { return }
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

    // Import: files the user placed in the local folder themselves are uploaded to the server.
    //
    // What counts as the user's own is decided by the local store, not by the server index:
    // a file no local copy is registered at. A registered copy is never new — not when the
    // server has since deleted or renamed the file, which the index alone would read as
    // "not on the server yet" and send a stale cache back up.
    func importLocalFiles() async {
        guard let local, let index, let client, session.isActive, !importing else { return }
        let session = self.session
        importing = true; defer { if session.isActive { importing = false } }
        // Nothing unclaimed and no copy still owed its server name is the usual case, and
        // costs no round trip.
        let seen = (try? local.unregisteredFiles()) ?? []
        let owed = (try? local.copiesAwaitingServerName()) ?? []
        guard !seen.isEmpty || !owed.isEmpty else { return }
        // Only against an index that is known to be current: a failed or skipped pull
        // leaves this for the next activation. Then look again — a download that finished
        // meanwhile has claimed its file.
        guard await refresh(), session.isActive else { return }
        settleImportedNames()
        let candidates = (try? local.unregisteredFiles()) ?? []
        var unnamed: [(file: LocalStore.UnregisteredFile, stamp: LocalStore.FileStamp)] = []
        var uploadedAny = false
        for f in candidates {
            guard session.isActive else { return }
            // An upload takes a while, and a download of this very path may have finished
            // during the ones before it: what is a registered copy by now is not imported.
            guard (try? local.isStillUnregistered(f)) == true, let stamp = local.stamp(of: f.url) else { continue }
            do {
                // "Create only if absent": whatever the index says, another client may have
                // taken the name a moment ago. Base version 0 matches no existing file, so
                // the server then keeps theirs and files ours beside it as a conflict copy.
                let outcome = try await session.perform {
                    try await client.upload(fileURL: f.url, relPath: f.relPath,
                                            modifiedAt: APIClient.contentModificationDate(of: f.url),
                                            baseVersion: ContentVersionCodec.unknownBase)
                }
                try session.check()
                uploadedAny = true
                // Claimed the moment the server has it, as the node and version the server
                // said it became — not after the refresh below, which may fail and would
                // leave the file to be uploaded again, and not as whatever version the
                // index holds by then, which may be someone else's later edit. A conflict
                // copy has another name on the server; the row remembers it is owed one.
                //
                // Only if the file at the path is still the one that was sent: a download
                // of this path during the upload leaves its own, registered, file there.
                if outcome.nodeID.isEmpty { unnamed.append((f, stamp)); continue }
                try? local.adoptUploaded(f, uploadedAs: stamp, nodeID: outcome.nodeID, version: outcome.version,
                                         awaitingServerName: outcome.conflicted)
            } catch { if session.isActive { fail("status.uploadError", error) } }
        }
        guard session.isActive else { return }
        guard uploadedAny else { revision += 1; return }
        if await refresh(), session.isActive {
            settleImportedNames()
            // The server did not say what the upload became: the index is all there is.
            for (f, stamp) in unnamed {
                guard let node = try? index.node(atPath: f.relPath) else { continue }
                try? local.adoptUploaded(f, uploadedAs: stamp, nodeID: node.id, version: node.version)
            }
        }
        if session.isActive { revision += 1 }
    }

    // Conflict copies the import registered under the name they were dropped in as take the
    // name the server gave them. Call only right after a successful refresh: a node the
    // index does not hold then is gone, not merely not heard of yet.
    private func settleImportedNames() {
        guard let local, let index else { return }
        for id in (try? local.copiesAwaitingServerName()) ?? [] {
            try? local.settleName(nodeID: id, serverPath: (try? index.node(id: id))?.path)
        }
    }
}
