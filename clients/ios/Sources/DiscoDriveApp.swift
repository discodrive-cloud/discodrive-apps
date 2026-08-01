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
                if app.paired { BrowserView() } else { PairingView() }
            }
            .environmentObject(app)
            .onAppear {
                app.bootstrap()
                // The service holds no reference to AppState: it asks for a client when it
                // needs one, so re-pairing cannot leave it talking to the old server.
                AutoUploadService.shared.configure { app.client }
                AutoUploadService.shared.resumeIfEnabled()
            }
        }
    }
}
