import SwiftUI
import DiscoKit
import FileProvider
import os

@main
struct DiscoDriveApp: App {
    @StateObject private var app = AppState()
    @StateObject private var fullSync = FullSyncController()
    @State private var reauthenticate = false
    @State private var checkingAuthentication = false
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    init() {
        // The App Group and keychain group are shared with the File Provider extension and
        // declared in Info.plist next to the entitlements, so the two cannot drift apart.
        let info = Bundle.main.infoDictionary ?? [:]
        KeychainConfig.accessGroup = info["DiscoDriveKeychainGroup"] as? String
        AppState.appGroupID = info["DiscoDriveAppGroup"] as? String
        // Strict TLS validation in every build; a self-signed server is accepted only by the
        // fingerprint trusted at pairing (DiscoNet.pin, set when the pairing is activated).
    }
    // discodrive://vault/open?id=<node>  → the unlock sheet for that folder
    // discodrive://vault/close?id=<node> → the vault's Finder location goes away
    private func handle(_ url: URL) {
        let scheme = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveURLScheme") as? String ?? "discodrive"
        guard url.scheme == scheme else { return }
        if url.host == "authenticate" { authenticateFromFinder(url); return }
        guard url.host == "vault",
              let id = URLComponents(url: url, resolvingAgainstBaseURL: false)?
                .queryItems?.first(where: { $0.name == "id" })?.value else { return }
        Logger(subsystem: "org.discodrive.app", category: "vault").notice("url \(url.path, privacy: .public) id \(id, privacy: .public)")
        switch url.path {
        case "/open":
            guard let node = app.node(id: id) else { return }
            appDelegate.showWindow()
            app.vaultUnlockFolder = node
        case "/close":
            let name = app.node(id: id)?.name ?? ""
            Task { await VaultDomains.close(vaultID: id, name: name) }   // refreshes the open list
        default: break
        }
    }

    private func authenticateFromFinder(_ url: URL) {
        appDelegate.showWindow()
        app.bootstrap()
        guard let client = app.client, !checkingAuthentication else { return }
        checkingAuthentication = true
        let domain = URLComponents(url: url, resolvingAgainstBaseURL: false)?
            .queryItems?.first(where: { $0.name == "domain" })?.value
        Task {
            defer { checkingAuthentication = false }
            do {
                await client.resetAuth()
                _ = try await client.authToken()
                guard app.client === client, app.paired else { return }
                await app.refresh()
                guard app.client === client, app.paired else { return }
                if let domain, domain.hasPrefix(VaultCoreDomainPrefix) {
                    let id = String(domain.dropFirst(VaultCoreDomainPrefix.count))
                    if VaultKeyStore.load(forVault: id) == nil, let node = app.node(id: id) {
                        app.vaultUnlockFolder = node
                    }
                }
            } catch {
                guard app.client === client, app.paired else { return }
                app.lastError = app.userMessage(for: error) ?? app.t("status.offline")
                if case .sessionExpired = AppState.kind(of: error) { reauthenticate = true }
            }
        }
    }

    var body: some Scene {
        WindowGroup("DiscoDrive") {
            Group {
                if app.paired { BrowserView() }
                else { PairingView() }
            }
            .frame(minWidth: 700, minHeight: 480)
            .environmentObject(app)
            .onAppear {
                if app.fileAccess == nil { app.fileAccess = FinderFileAccess() }
                app.bootstrap()
                // On this platform an unlocked vault is a Finder location, not a window.
                app.presentUnlockedVault = { vault, folder in
                    await VaultDomains.open(vaultID: folder.id, name: folder.name, keys: vault.rawKeys)
                }
                // Which vaults are open is the system's list of their domains, re-read
                // after every open/close and when the app comes back to the front (a
                // vault may have been closed from Finder's menu meanwhile).
                VaultDomains.didChange = {
                    Task { @MainActor in app.openVaultIDs = await VaultDomains.openVaultIDs() }
                }
                VaultDomains.didChange?()
                // Finder shows the DiscoDrive folder while paired; the extension is
                // nudged after every change the server streams to the app.
                app.onRemoteChange = { FileProviderDomain.signalChanges() }
                // Leaving the account closes the vaults open in Finder first: their
                // domains, keys and decrypted files are the old account's. One left over
                // from a run that did not get to close it goes the same way.
                app.beforeLogout = {
                    await fullSync.logout()
                    return await FileProviderDomain.closeForLogout()
                }
                appDelegate.beforeQuit = { await fullSync.quit() }
                fullSync.attach(app)
                #if DEBUG
                if ProcessInfo.processInfo.environment["DISCODRIVE_TEST_REPAIR"] == "1" {
                    // A test re-pair: Finder's copy of the old account goes with the old domain.
                    FileProviderDomain.unregister { if app.paired { FileProviderDomain.register() } }
                } else if app.paired { FileProviderDomain.register() }
                #else
                if app.paired { FileProviderDomain.register() }
                #endif
                appDelegate.urlHandler = { url in handle(url) }
            }
            .alert(app.t("dialog.logoutTitle"), isPresented: $reauthenticate) {
                Button(app.t("toolbar.logout"), role: .destructive) { app.logout() }
                Button(app.t("dialog.cancel"), role: .cancel) {}
            } message: {
                Text(app.t("status.sessionExpired") + "\n\n" + app.t("dialog.logoutMessage"))
            }
            .onChange(of: app.paired) { _, paired in
                if paired { FileProviderDomain.register(); fullSync.attach(app) }
            }
            .onReceive(NotificationCenter.default.publisher(for: .fileProviderMaterializedSetDidChange)) { _ in
                app.revision += 1
            }
            .onChange(of: app.syncStatus) { _, status in appDelegate.setStatus(status) }
            // Launched while the keychain was unavailable (screen locked at login): the
            // pairing is looked up again once the app is in front, before anyone re-pairs.
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                if !app.paired { app.bootstrap() }
                app.revision += 1
                VaultDomains.didChange?()
            }
        }
        // discodrive:// URLs go to the delegate; without this the group would open a
        // fresh window for each of them.
        .handlesExternalEvents(matching: [])
        .commands {
            CommandGroup(replacing: .appInfo) {
                Button("About DiscoDrive") { appDelegate.showAboutPanel() }
            }
        }
        Settings {
            SettingsView().environmentObject(app).environmentObject(fullSync)
        }
    }
}
