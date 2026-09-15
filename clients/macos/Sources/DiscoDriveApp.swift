import SwiftUI
import DiscoKit
import os

@main
struct DiscoDriveApp: App {
    @StateObject private var app = AppState()
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    init() {
        // The App Group and keychain group are shared with the File Provider extension and
        // declared in Info.plist next to the entitlements, so the two cannot drift apart.
        let info = Bundle.main.infoDictionary ?? [:]
        KeychainConfig.accessGroup = info["DiscoDriveKeychainGroup"] as? String
        AppState.appGroupID = info["DiscoDriveAppGroup"] as? String
        #if DEBUG
        // Debug builds may talk to a self-hosted server with a self-signed cert.
        // Release builds keep strict TLS validation.
        DiscoNet.allowInsecureTLS = true
        #endif
    }
    // discodrive://vault/open?id=<node>  → the unlock sheet for that folder
    // discodrive://vault/close?id=<node> → the vault's Finder location goes away
    private func handle(_ url: URL) {
        guard url.scheme == "discodrive", url.host == "vault",
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
            Task { await VaultDomains.close(vaultID: id, name: name) }
        default: break
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
                app.bootstrap()
                // On this platform an unlocked vault is a Finder location, not a window.
                app.presentUnlockedVault = { vault, folder in
                    await VaultDomains.open(vaultID: folder.id, name: folder.name, keys: vault.rawKeys)
                }
                // Finder shows the DiscoDrive folder while paired; the extension is
                // nudged after every change the server streams to the app.
                app.onRemoteChange = { FileProviderDomain.signalChanges() }
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
            .onChange(of: app.paired) { _, paired in
                paired ? FileProviderDomain.register() : FileProviderDomain.unregister()
            }
            .onChange(of: app.syncStatus) { _, status in appDelegate.setStatus(status) }
            // Launched while the keychain was unavailable (screen locked at login): the
            // pairing is looked up again once the app is in front, before anyone re-pairs.
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                if !app.paired { app.bootstrap() }
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
            SettingsView().environmentObject(app)
        }
    }
}
