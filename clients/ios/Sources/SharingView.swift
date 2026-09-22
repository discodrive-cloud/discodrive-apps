import SwiftUI
import DiscoKit

struct SharingView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let node: Node
    @State private var byLink = true
    @State private var email = ""
    @State private var expiry = 7
    @State private var shares: [APIClient.Share] = []
    @State private var link: URL?
    @State private var createdID: String?
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text(node.name).textSelection(.enabled)
                    Picker(app.t("share.method"), selection: $byLink) {
                        Text(app.t("share.link")).tag(true)
                        Text(app.t("share.user")).tag(false)
                    }
                    if !byLink {
                        TextField(app.t("share.email"), text: $email)
                            .textContentType(.emailAddress).keyboardType(.emailAddress)
                            .textInputAutocapitalization(.never).autocorrectionDisabled()
                    }
                    Picker(app.t("share.expiry"), selection: $expiry) {
                        Text(app.t("share.forever")).tag(0)
                        Text(app.t("share.day")).tag(1)
                        Text(app.t("share.week")).tag(7)
                        Text(app.t("share.month")).tag(30)
                    }
                    Button(app.t(byLink ? "share.create" : "share.grant")) { Task { await create() } }
                        .disabled(busy || (!byLink && email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty))
                } footer: { Text(app.t("share.readOnly")) }
                if let link {
                    Section {
                        ShareLink(item: link) { Label(app.t("share.send"), systemImage: "square.and.arrow.up") }
                        Button(app.t("share.copy")) { UIPasteboard.general.url = link }
                    }
                }
                Section {
                    if busy { ProgressView() }
                    if let error { Text(error).foregroundStyle(.red) }
                    if shares.isEmpty && !busy && error == nil { Text(app.t("share.empty")).foregroundStyle(.secondary) }
                    ForEach(shares) { share in
                        VStack(alignment: .leading, spacing: 6) {
                            Text(share.email ?? app.t("share.link"))
                            if let expires = share.expires_at {
                                Text(expires).font(.caption).foregroundStyle(.secondary)
                            }
                            Button(app.t("share.revoke"), role: .destructive) { Task { await revoke(share) } }.disabled(busy)
                        }
                    }
                } header: { Text(app.t("share.existing")) }
            }
            .navigationTitle(app.t("share.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button(app.t("dialog.done")) { dismiss() } } }
            .interactiveDismissDisabled(busy)
        }
        .task { await reload() }
        .onChange(of: app.paired) { _, _ in dismiss() }
    }

    private func reload() async {
        guard app.client != nil else { return }
        busy = true; defer { busy = false }
        do { shares = try await app.performAccountOperation { client in try await client.shares(nodeID: node.id) }; error = nil }
        catch { self.error = app.userMessage(for: error) ?? app.t("status.opError") }
    }
    private func create() async {
        guard !busy, let client = app.client, app.paired, !app.loggingOut else { return }
        busy = true; error = nil; defer { busy = false }
        do {
            let recipient = byLink ? nil : email.trimmingCharacters(in: .whitespacesAndNewlines)
            let seconds = expiry == 0 ? nil : expiry * 86400
            let result = try await app.performAccountOperation { client in try await client.share(nodeID: node.id, email: recipient, expiresInSeconds: seconds) }
            guard app.client === client, app.paired else { return }
            if let token = result.token { link = await client.shareURL(token: token); createdID = result.share_id }
            shares = try await app.performAccountOperation { client in try await client.shares(nodeID: node.id) }
        } catch { self.error = app.userMessage(for: error) ?? app.t("status.opError") }
    }
    private func revoke(_ share: APIClient.Share) async {
        guard !busy, app.client != nil, app.paired, !app.loggingOut else { return }
        busy = true; error = nil; defer { busy = false }
        do {
            try await app.performAccountOperation { client in try await client.revokeShare(id: share.id) }
            if createdID == share.id { link = nil; createdID = nil }
            shares = try await app.performAccountOperation { client in try await client.shares(nodeID: node.id) }
        } catch { self.error = app.userMessage(for: error) ?? app.t("status.opError") }
    }
}
