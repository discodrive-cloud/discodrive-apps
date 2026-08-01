import SwiftUI
import DiscoKit

@main
struct DiscoDriveApp: App {
    @StateObject private var app = AppState()

    init() {
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
                if ProcessInfo.processInfo.environment["DISCODRIVE_TEST_SCREEN"] == "autoupload", app.paired {
                    NavigationStack { AutoUploadView() }
                } else if app.paired { BrowserView() } else { PairingView() }
                #else
                if app.paired { BrowserView() } else { PairingView() }
                #endif
            }
            .environmentObject(app)
            .onAppear {
                app.bootstrap()
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
