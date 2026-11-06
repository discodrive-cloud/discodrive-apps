import SwiftUI
import DiscoKit

struct PairingView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.openURL) private var openURL
    @State private var serverString = "https://"
    @State private var info: PairingInfo?
    @State private var busy = false
    @State private var error: String?
    /// A verification link that failed URLPolicy: shown for copying, never opened.
    @State private var manualLink: String?
    /// The server's sign-in page, shown inside the app (App Store guideline 4: no trip to
    /// the default browser). Kept after the sheet is closed so it can be reopened.
    @State private var signInURL: URL?
    @State private var presentedSignIn: PresentedURL?
    /// The server's certificate, offered for trust after a strict attempt failed on it.
    @State private var certificate: CertificateInfo?
    /// The server `certificate` was read from: a trusted pin is only ever used for it.
    @State private var certificateURL: URL?

    var body: some View {
        VStack(spacing: 18) {
            // The artwork the Android client uses, rather than an SF Symbol standing in for it.
            Image("DiscLogo").resizable().scaledToFit().frame(width: 96, height: 96)
            Text(app.t("pair.title")).font(.title2.bold())
            TextField("https://files.example.com", text: $serverString)
                .textFieldStyle(.roundedBorder)
                .onSubmit {
                    guard !busy, info == nil, certificate == nil else { return }
                    Task { await connect() }
                }
                .submitLabel(.go)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .keyboardType(.URL)
                .padding(.horizontal, 32)
            if let info {
                Text("\(app.t("pair.deviceCode")): \(info.userCode)").font(.headline)
                Text(app.t(signInURL == nil ? "pair.confirmHint" : "pair.confirmHintInApp"))
                    .multilineTextAlignment(.center).foregroundStyle(.secondary).padding(.horizontal)
                if let signInURL, presentedSignIn == nil {
                    Button(app.t("pair.openSignIn")) { presentedSignIn = PresentedURL(url: signInURL) }
                        .buttonStyle(.bordered)
                }
                if let manualLink {
                    Text(app.t("pair.openManually"))
                        .multilineTextAlignment(.center).font(.caption).padding(.horizontal)
                    Text(manualLink).font(.caption.monospaced()).textSelection(.enabled)
                        .multilineTextAlignment(.center).padding(.horizontal)
                }
                ProgressView()
            } else {
                Button(busy ? "…" : app.t("pair.connect")) { Task { await connect() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(busy || URL(string: serverString) == nil)
            }
            // app.lastError: a stored pairing refused at launch (e.g. a plain http address).
            if let error = error ?? app.lastError { Text(error).foregroundStyle(.red).font(.caption) }
        }
        .padding()
        .sheet(item: $presentedSignIn) { SafariView(url: $0.url).ignoresSafeArea() }
        .sheet(isPresented: Binding(get: { certificate != nil }, set: { if !$0 { certificate = nil } })) {
            if let certificate {
                CertificateTrustView(certificate: certificate, onTrust: trust, onCancel: { self.certificate = nil })
                    .environmentObject(app)
            }
        }
    }

    private func trust() {
        guard let certificate, let url = certificateURL else { return }
        self.certificate = nil
        Task { await connect(url: url, pin: certificate.fingerprint) }
    }

    // `pin` is nil for the strict first attempt, else the fingerprint of the certificate
    // just fetched from `url` and trusted by the user.
    private func connect(url given: URL? = nil, pin: String? = nil) async {
        guard !busy else { return }
        guard let url = given ?? URL(string: serverString) else { return }
        busy = true; error = nil; manualLink = nil
        do {
            let info = try await app.startPairing(serverURL: url, pin: pin)
            self.info = info
            // Only a web page on the server just typed in is shown; any other scheme or host
            // would reach whatever app claims it.
            if let v = URL(string: info.verificationURI, relativeTo: url)?.absoluteURL,
               URLPolicy.isOpenable(v, relativeTo: url) {
                // In the app, via Safari View Controller. It cannot accept a certificate the
                // user pinned by hand, so such a server still gets the system browser.
                if pin == nil { signInURL = v; presentedSignIn = PresentedURL(url: v) } else { openURL(v) }
            } else { manualLink = info.verificationURI }
            try await app.confirmPairing(serverURL: url, info: info, pin: pin)
            presentedSignIn = nil; signInURL = nil
        } catch {
            presentedSignIn = nil; signInURL = nil
            self.info = nil
            if pin == nil, !(error is CancellationError), let untrusted = await app.untrustedCertificate(for: url) {
                certificateURL = url
                certificate = untrusted
            } else {
                self.error = app.pairingMessage(for: error, serverURL: url)
            }
        }
        manualLink = nil
        busy = false
    }
}
