import SwiftUI
import Kfmobile

struct SetupView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.openURL) private var openURL
    @State private var server = "https://"
    /// A verification link that is not a web page on the server: shown, never opened.
    @State private var manualLink: String?

    var body: some View {
        VStack(spacing: 18) {
            Image(systemName: "arrow.triangle.2.circlepath").font(.system(size: 52)).foregroundStyle(.tint)
            Text("DiscoDrive FastSync").font(.title2.bold())
            TextField("https://files.example.com", text: $server)
                .textFieldStyle(.roundedBorder)
                .autocorrectionDisabled().textInputAutocapitalization(.never).keyboardType(.URL)
            if let code = model.pendingUserCode {
                Text("Code: \(code)").font(.headline)
                Text("Confirm this code in the browser to pair.")
                    .foregroundStyle(.secondary).multilineTextAlignment(.center)
                if let manualLink {
                    Text("The server sent a link that was not opened automatically. If you trust it, copy it into a browser:")
                        .font(.caption).multilineTextAlignment(.center)
                    Text(manualLink).font(.caption.monospaced()).textSelection(.enabled).multilineTextAlignment(.center)
                }
                ProgressView()
            } else {
                Button(model.working ? "…" : "Pair device") { Task { await pair() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.working || URL(string: server) == nil)
            }
            if let e = model.lastError { Text(e).foregroundStyle(.red).font(.caption) }
        }
        .padding(28)
        .sheet(isPresented: Binding(get: { model.pendingCertificate != nil },
                                    set: { if !$0 { model.pendingCertificate = nil } })) {
            if let cert = model.pendingCertificate {
                CertificateTrustSheet(certificate: cert, onTrust: trust, onCancel: { model.pendingCertificate = nil })
            }
        }
    }

    private func trust() {
        guard let cert = model.pendingCertificate, let server = model.certificateServer else { return }
        model.pendingCertificate = nil
        Task { await pair(server: server, pin: cert.fingerprint) }
    }

    // `pin` is "" for the strict first attempt, else the fetched and trusted fingerprint.
    private func pair(server given: String? = nil, pin: String = "") async {
        let server = given ?? self.server
        guard let p = await model.startPairing(server: server, pin: pin) else { return }
        manualLink = nil
        if let base = URL(string: server), let u = URL(string: p.verificationURL, relativeTo: base)?.absoluteURL,
           Self.isOpenable(u, relativeTo: base) { openURL(u) }
        else { manualLink = p.verificationURL }
        await model.finishPairing(server: server, pairing: p, pin: pin)
        manualLink = nil
    }

    /// Only a web page on the server being paired is handed to the system: any other scheme
    /// or host would reach whatever app claims it. Mirrors DiscoKit's URLPolicy, which this
    /// app does not link.
    static func isOpenable(_ url: URL, relativeTo server: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(), scheme == "https" || scheme == "http",
              let serverScheme = server.scheme?.lowercased(),
              let host = url.host?.lowercased(), !host.isEmpty,
              let serverHost = server.host?.lowercased(), !serverHost.isEmpty,
              scheme == serverScheme else { return false }
        func port(_ u: URL, _ s: String) -> Int? { u.port ?? (s == "https" ? 443 : s == "http" ? 80 : nil) }
        return host == serverHost && port(url, scheme) == port(server, serverScheme)
    }
}

// Shown when pairing failed because the system does not trust the server's certificate.
struct CertificateTrustSheet: View {
    let certificate: MobileCertificate
    let onTrust: () -> Void
    let onCancel: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                Label("Untrusted server certificate", systemImage: "exclamationmark.shield").font(.headline)
                if certificate.selfSigned {
                    Text("Self-signed").font(.caption.bold())
                        .padding(.horizontal, 8).padding(.vertical, 3)
                        .background(Capsule().fill(Color.orange.opacity(0.18)))
                }
                field("Server", certificate.host)
                VStack(alignment: .leading, spacing: 4) {
                    Text("SHA-256 fingerprint").font(.caption).foregroundStyle(.secondary)
                    Text(certificate.fingerprint).font(.callout.monospaced()).textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !certificate.subject.isEmpty { field("Issued to", certificate.subject) }
                if !certificate.issuer.isEmpty { field("Issued by", certificate.issuer) }
                if let expires = ISO8601DateFormatter().date(from: certificate.notAfter) {
                    field("Expires", expires.formatted(date: .long, time: .omitted))
                }
                Text("Only trust this if it matches the fingerprint of your server's certificate.")
                    .font(.callout).fixedSize(horizontal: false, vertical: true)
                HStack {
                    Spacer()
                    Button("Cancel", role: .cancel, action: onCancel)
                    Button("Trust", action: onTrust).buttonStyle(.borderedProminent)
                }
            }
            .padding(24)
        }
    }

    private func field(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(label).font(.caption).foregroundStyle(.secondary)
            Text(value).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        }
    }
}
