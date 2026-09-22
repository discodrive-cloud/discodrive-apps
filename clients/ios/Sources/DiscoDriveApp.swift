import SwiftUI
import DiscoKit

@main
struct DiscoDriveApp: App {
    @StateObject private var app: AppState
    @StateObject private var files: FilesIntegration
    @StateObject private var fullSync: FullSyncController
    @Environment(\.scenePhase) private var scenePhase

    init() {
        KeychainConfig.accessGroup = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveKeychainGroup") as? String
        AppState.appGroupID = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveAppGroup") as? String
        try? FileManager.default.removeItem(at: FileManager.default.temporaryDirectory.appendingPathComponent("VaultPreviews"))
        let state = AppState()
        let integration = FilesIntegration()
        let sync = FullSyncController()
        integration.attach(state)
        sync.attach(state)
        state.beforeLogout = {
            await sync.logout()
            await AutoUploadService.shared.logout()
            return await integration.disconnect()
        }
        _app = StateObject(wrappedValue: state)
        _files = StateObject(wrappedValue: integration)
        AutoUploadService.shared.configure { [weak state] in state?.client }
        state.bootstrap()
        sync.registerBackgroundTask()
        _fullSync = StateObject(wrappedValue: sync)
        // Must be registered before the app finishes launching, or iOS refuses the handler.
        AutoUploadService.shared.registerBackgroundTask()
        #if DEBUG
        // Debug builds may talk to a self-hosted server with a self-signed cert.
        // Release builds keep strict TLS validation.
        DiscoNet.allowInsecureTLS = true
        #endif
    }

    var body: some Scene {
        WindowGroup {
            Group {
                #if DEBUG
                // Automated runs cannot tap: this opens a screen directly so a screenshot
                // can show it, and is ignored unless the variable is set. Debug only.
                if ProcessInfo.processInfo.environment["DISCODRIVE_TEST_SCREEN"] == "settings" {
                    SettingsView()
                } else if ProcessInfo.processInfo.environment["DISCODRIVE_TEST_SCREEN"] == "autoupload", app.paired {
                    NavigationStack { AutoUploadView() }
                } else if app.paired { BrowserView() } else { PairingView() }
                #else
                if app.paired { BrowserView() } else { PairingView() }
                #endif
            }
            .onOpenURL { url in
                if url.scheme == "discodrive-ios", url.host == "authenticate" { Task { await files.authenticate(url) } }
            }
            .environmentObject(app)
            .environmentObject(files)
            .environmentObject(fullSync)
            .onChange(of: app.paired) { _, paired in
                Task { if paired { await files.connect(); await fullSync.resume() } else { _ = await files.disconnect() } }
            }
            .onChange(of: scenePhase) { _, phase in
                Task {
                    if phase == .active { await fullSync.resume(); if await app.refresh() { await files.signal() } }
                    else if phase == .background { await fullSync.suspend() }
                }
            }
            .onAppear {
                Task {
                    if app.paired { await files.connect(); if scenePhase == .active { await fullSync.resume() } }
                    else { _ = await files.disconnect() }
                }
                // The service holds no reference to AppState: it asks for a client when it
                // needs one, so re-pairing cannot leave it talking to the old server.
                AutoUploadService.shared.configure { app.client }
                AutoUploadService.shared.resumeIfEnabled()
                #if DEBUG
                // Drives one pass end to end for automated checks, since the simulator
                // cannot flip the switch by hand.
                if ProcessInfo.processInfo.environment["DISCODRIVE_TEST_AUTOUPLOAD"] == "1" {
                    Task {
                        let status = await PhotoLibrarySource.requestAccess()
                        await AutoUploadService.shared.setEnabled(true)
                        let r = await AutoUploadService.shared.runPass()
                        // A background launch swallows stdout, so the result goes to a file
                        // the harness can read out of the app container.
                        // setEnabled already ran a pass, so `r` is the second one and
                        // reads zero. The journal totals are what actually happened.
                        let totals = try? AutoUploadService.shared.openJournal().counts()
                        let line = """
                        photos=\(status.rawValue) enabled=\(AutoUploadSettings.shared.enabled) \
                        journal_sent=\(totals?.sent ?? -1) journal_skipped=\(totals?.skipped ?? -1) \
                        journal_deferred=\(totals?.deferred ?? -1) \
                        lastpass_blocked=\(r.blocked) lastpass_error=\(r.error ?? "-")
                        """
                        let out = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
                            .appendingPathComponent("autoupload_result.txt")
                        try? line.write(to: out, atomically: true, encoding: .utf8)
                    }
                }
                #endif
            }
        }
    }
}
