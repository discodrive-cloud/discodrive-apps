import SwiftUI
import DiscoKit

struct PairingView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.openURL) private var openURL
    @State private var serverString = "https://"
    @State private var info: PairingInfo?
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 18) {
            // The artwork the Android client uses, rather than an SF Symbol standing in for it.
            Image("DiscLogo").resizable().scaledToFit().frame(width: 96, height: 96)
            Text(app.t("pair.title")).font(.title2.bold())
            TextField("https://files.example.com", text: $serverString)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                .padding(.horizontal, 32)
            if let info {
                Text("\(app.t("pair.deviceCode")): \(info.userCode)").font(.headline)
                Text(app.t("pair.confirmHint"))
                    .multilineTextAlignment(.center).foregroundStyle(.secondary).padding(.horizontal)
                ProgressView()
            } else {
                Button(busy ? "…" : app.t("pair.connect")) { Task { await connect() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(busy || URL(string: serverString) == nil)
            }
            if let error { Text(error).foregroundStyle(.red).font(.caption) }
        }
        .padding()
    }

    private func connect() async {
        guard let url = URL(string: serverString) else { return }
        busy = true; error = nil
        do {
            let info = try await app.startPairing(serverURL: url)
            self.info = info
            if let v = URL(string: info.verificationURI, relativeTo: url)?.absoluteURL { openURL(v) }
            try await app.confirmPairing(serverURL: url, info: info)
        } catch { self.error = error.localizedDescription; self.info = nil }
        busy = false
    }
}
