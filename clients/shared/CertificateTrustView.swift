import SwiftUI
import DiscoKit

// Shown when pairing failed because the system does not trust the server's certificate:
// the user compares the fingerprint with their server's and decides whether to trust it.
struct CertificateTrustView: View {
    @EnvironmentObject var app: AppState
    let certificate: CertificateInfo
    let onTrust: () -> Void
    let onCancel: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 8) {
                    Image(systemName: "exclamationmark.shield").foregroundStyle(.orange)
                    Text(app.t("pairing.certTitle")).font(.headline)
                }
                if certificate.selfSigned {
                    Text(app.t("pairing.certSelfSigned"))
                        .font(.caption.bold())
                        .padding(.horizontal, 8).padding(.vertical, 3)
                        .background(Capsule().fill(Color.orange.opacity(0.18)))
                }
                field("pairing.certHost", certificate.host)
                VStack(alignment: .leading, spacing: 4) {
                    Text(app.t("pairing.certFingerprint")).font(.caption).foregroundStyle(.secondary)
                    Text(certificate.fingerprint)
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if !certificate.subject.isEmpty { field("pairing.certSubject", certificate.subject) }
                if !certificate.issuer.isEmpty { field("pairing.certIssuer", certificate.issuer) }
                if let notAfter = certificate.notAfter {
                    field("pairing.certExpires", notAfter.formatted(date: .long, time: .omitted))
                }
                Text(app.t("pairing.certHint"))
                    .font(.callout)
                    .fixedSize(horizontal: false, vertical: true)
                HStack {
                    Spacer()
                    Button(app.t("dialog.cancel"), role: .cancel, action: onCancel)
                        .keyboardShortcut(.cancelAction)
                    Button(app.t("pairing.certTrust"), action: onTrust)
                        .buttonStyle(.borderedProminent)
                }
            }
            .padding(24)
        }
        #if os(macOS)
        .frame(width: 460)
        .frame(minHeight: 340)
        #endif
    }

    private func field(_ key: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(app.t(key)).font(.caption).foregroundStyle(.secondary)
            Text(value).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
        }
    }
}
