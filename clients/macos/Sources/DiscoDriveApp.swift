import SwiftUI
import DiscoKit

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
    var body: some Scene {
        WindowGroup("DiscoDrive") {
            Group {
                if app.paired { BrowserView() }
                else { PairingView() }
            }
            .frame(minWidth: 700, minHeight: 480)
            .environmentObject(app)
            .onAppear { app.bootstrap() }
            .onChange(of: app.syncStatus) { _, status in appDelegate.setStatus(status) }
        }
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
