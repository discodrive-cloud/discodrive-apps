import SwiftUI
import CryptoKit
import DiscoKit
#if os(macOS)
import AppKit
#else
import UIKit
#endif

struct DAVSetupView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @State private var access: APIClient.DAVAccess?
    @State private var calendars = true
    @State private var contacts = true
    @State private var credential: APIClient.DAVCredential?
    @State private var service: String?
    @State private var profileURL: URL?
    @State private var busy = false
    @State private var error: String?
    @State private var copied = false
    @State private var confirmRevoke = false
    @State private var automaticProfile = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Toggle(app.t("dav.calendars"), isOn: $calendars).disabled(access?.caldav != true || busy)
                    Toggle(app.t("dav.contacts"), isOn: $contacts).disabled(access?.carddav != true || busy)
                    if access != nil && (access?.caldav != true || access?.carddav != true) {
                        Text(app.t("dav.disabled")).foregroundStyle(.secondary)
                    }
                    Button(app.t("dav.prepare")) { Task { await prepare() } }
                        .disabled(busy || service == nil || (!calendars && !contacts))
                } footer: { Text(app.t("dav.hint")) }
                #if os(iOS)
                Section {
                    Text(app.t("dav.profileSignatureHint"))
                        .font(.callout)
                        .fixedSize(horizontal: false, vertical: true)
                    Button(app.t("dav.automatic")) { Task { await prepare(automatic: true) } }
                        .disabled(busy || service == nil || (!calendars && !contacts))
                } footer: { Text(app.t("dav.automaticHint")) }
                #endif
                if credential != nil {
                    Section {
                        if !automaticProfile {
                            Button(app.t(copied ? "dav.copied" : "dav.copyPassword")) { copyPassword() }
                        }
                        if let profileURL {
                            Button(app.t("dav.install")) { openURL(profileURL) }
                        } else {
                            Text(app.t("dav.prepareAgain")).foregroundStyle(.secondary)
                        }
                        Text(app.t(automaticProfile ? "dav.automaticInstallHint" : "dav.installHint")).font(.callout).foregroundStyle(.secondary)
                    }
                    Section {
                        Button(app.t("dav.revoke"), role: .destructive) { confirmRevoke = true }.disabled(busy)
                    } footer: { Text(app.t("dav.independent")) }
                }
                if busy { ProgressView() }
                if let error {
                    Text(error).foregroundStyle(.red)
                    if service == nil { Button(app.t("dav.retry")) { Task { await load() } }.disabled(busy) }
                }
            }
            .navigationTitle(app.t("dav.title"))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #else
            .formStyle(.grouped)
            #endif
            .toolbar { ToolbarItem(placement: .confirmationAction) {
                Button(app.t("dialog.done")) { dismiss() }.disabled(busy)
            } }
            .interactiveDismissDisabled(busy)
            .alert(app.t("dav.revoke"), isPresented: $confirmRevoke) {
                Button(app.t("dav.revoke"), role: .destructive) { Task { await revoke() } }
                Button(app.t("dialog.cancel"), role: .cancel) {}
            } message: { Text(app.t("dav.revokeHint")) }
        }
        #if os(macOS)
        .frame(width: 500, height: 570)
        #endif
        .task { await load() }
        .onChange(of: calendars) { _, _ in profileURL = nil }
        .onChange(of: contacts) { _, _ in profileURL = nil }
        .onChange(of: app.paired) { _, _ in dismiss() }
    }

    private func load() async {
        busy = true; error = nil; defer { busy = false }
        do {
            let (account, access) = try await app.performAccountOperation { client in
                (try await client.davAccount(), try await client.davAccess())
            }
            guard let server = app.serverURL else { return }
            let hash = SHA256.hash(data: Data((server.absoluteString + "\n" + account.id).utf8))
                .map { String(format: "%02x", $0) }.joined()
            let key = KeychainToken.davServicePrefix + hash
            service = key
            self.access = access
            calendars = access.caldav; contacts = access.carddav
            if let stored = KeychainToken.load(service: key), let data = stored.data(using: .utf8) {
                credential = try JSONDecoder().decode(APIClient.DAVCredential.self, from: data)
            }
        } catch { show(error) }
    }

    private func prepare(automatic: Bool = false) async {
        guard let service, !busy else { return }
        busy = true; error = nil; profileURL = nil; defer { busy = false }
        let selectedCalendars = calendars, selectedContacts = contacts
        let existing = credential
        do {
            let result = try await app.performAccountOperation { client in
                // Check server support before creating a password. The installation key is
                // stable for this app/account, so reinstalling replaces the same profile.
                if automatic {
                    guard try await client.appleEnrollmentAvailable() else { throw APIError.http(404) }
                }
                let manualURL = automatic ? nil : try await client.appleProfile(installationID: service, calendars: selectedCalendars, contacts: selectedContacts)
                if let existing {
                    let url: URL
                    if let manualURL { url = manualURL }
                    else { url = try await client.appleEnrollment(installationID: service, calendars: selectedCalendars, contacts: selectedContacts, credential: existing) }
                    return (url, existing)
                }
                #if os(macOS)
                let name = "DiscoDrive · macOS calendars and contacts"
                #else
                let name = "DiscoDrive · iOS calendars and contacts"
                #endif
                let created = try await client.createDAVPassword(name: name)
                let data = try JSONEncoder().encode(created)
                guard let json = String(data: data, encoding: .utf8), KeychainToken.save(json, service: service) else {
                    try? await client.revokeDAVPassword(id: created.id)
                    throw APIError.badResponse
                }
                let url: URL
                if let manualURL { url = manualURL }
                else { url = try await client.appleEnrollment(installationID: service, calendars: selectedCalendars, contacts: selectedContacts, credential: created) }
                return (url, created)
            }
            profileURL = result.0; credential = result.1; automaticProfile = automatic
            if automatic { openURL(result.0) }
        } catch {
            // A failed enrollment can follow successful credential creation. Keep
            // revocation/retry available without creating a second password.
            if let stored = KeychainToken.load(service: service), let data = stored.data(using: .utf8) {
                credential = try? JSONDecoder().decode(APIClient.DAVCredential.self, from: data)
            }
            if automatic, case APIError.http(404) = error { self.error = app.t("dav.automaticUnavailable") }
            else { show(error) }
        }
    }

    private func copyPassword() {
        guard let credential else { return }
        #if os(macOS)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(credential.password, forType: .string)
        #else
        UIPasteboard.general.setItems([[UIPasteboard.typeAutomatic: credential.password]],
            options: [.localOnly: true, .expirationDate: Date().addingTimeInterval(600)])
        #endif
        copied = true
    }

    private func revoke() async {
        guard let credential, let service, !busy else { return }
        busy = true; error = nil; defer { busy = false }
        do {
            try await app.performAccountOperation { client in try await client.revokeDAVPassword(id: credential.id) }
            KeychainToken.delete(service: service)
            self.credential = nil; profileURL = nil; copied = false
        } catch { show(error) }
    }

    private func show(_ error: Error) {
        if case APIError.http(404) = error { self.error = app.t("dav.updateServer") }
        else { self.error = app.userMessage(for: error) ?? app.t("status.opError") }
    }
}
