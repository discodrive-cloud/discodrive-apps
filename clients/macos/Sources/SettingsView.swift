import SwiftUI
import DiscoKit
import ServiceManagement
import os

struct SettingsView: View {
    @EnvironmentObject var app: AppState
    @State private var activityPresented = false
    @State private var davPresented = false
    @EnvironmentObject var fullSync: FullSyncController
    @State private var loginStatus = SMAppService.mainApp.status
    @State private var loginItemFailed = false
    @State private var confirmDeletion = false

    private func setLaunchAtLogin(_ enabled: Bool) {
        loginItemFailed = false
        defer { loginStatus = SMAppService.mainApp.status }
        do {
            if enabled { try SMAppService.mainApp.register() }
            else { try SMAppService.mainApp.unregister() }
        } catch {
            loginItemFailed = true
            Logger(subsystem: "org.discodrive.app", category: "settings")
                .error("Login item update failed: \(String(describing: error), privacy: .public)")
        }
    }

    var body: some View {
        Form {
            Picker(app.t("settings.language"), selection: Binding(
                get: { app.language },
                set: { newLang in Task { await app.setLanguage(newLang) } }
            )) {
                ForEach(L10n.supported, id: \.self) { code in
                    Text(L10n.displayName[code] ?? code).tag(code)
                }
            }
            Toggle(app.t("settings.launchAtLogin"), isOn: Binding(
                get: { loginStatus == .enabled || loginStatus == .requiresApproval },
                set: setLaunchAtLogin
            ))
            if loginStatus == .requiresApproval {
                Text(app.t("settings.loginApproval"))
                    .font(.callout)
                    .foregroundStyle(.secondary)
                Button(app.t("settings.openLoginItems")) {
                    SMAppService.openSystemSettingsLoginItems()
                }
            }
            if loginItemFailed {
                Text(app.t("settings.loginError"))
                    .font(.callout)
                    .foregroundStyle(.red)
            }
            Section {
                Button(app.t("dav.title")) { davPresented = true }.disabled(!app.paired)
            }
            Section {
                GroupBox(app.t("fullSync.title")) {
                    VStack(alignment: .leading, spacing: 10) {
                        Text(app.t("fullSync.description")).font(.callout).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                        Text(fullSync.folder?.path ?? app.t("fullSync.noFolder"))
                            .font(.callout).textSelection(.enabled).lineLimit(3)
                        Button(app.t("fullSync.choose")) { fullSync.chooseFolder() }
                            .disabled(fullSync.enabled || fullSync.busy)
                        Toggle(app.t("fullSync.enable"), isOn: Binding(
                            get: { fullSync.enabled }, set: { fullSync.setEnabled($0) }
                        )).disabled(fullSync.busy || (!fullSync.enabled && (fullSync.folder == nil || !app.paired)))
                        Text(app.t(fullSync.statusKey)).font(.callout).foregroundStyle(.secondary)
                        Button(app.t("activity.title")) { activityPresented = true }
                        if fullSync.statusKey == "fullSync.bulkDelete" {
                            Button(app.t("fullSync.confirmDelete"), role: .destructive) { confirmDeletion = true }
                        }
                        if let backup = fullSync.backup {
                            Button(app.t("fullSync.backup")) { NSWorkspace.shared.open(backup) }
                        }
                        Text(app.t("fullSync.backupHint")).font(.caption).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }.frame(maxWidth: .infinity, alignment: .leading).padding(6)
                }
            }
        }
        .sheet(isPresented: $activityPresented) { SyncActivityView() }
        .sheet(isPresented: $davPresented) { DAVSetupView() }
        .alert(app.t("fullSync.confirmDelete"), isPresented: $confirmDeletion) {
            Button(app.t("fullSync.confirmDelete"), role: .destructive) { fullSync.confirmDeletion() }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        } message: { Text(app.t("fullSync.deleteWarning")) }
        .onAppear { loginStatus = SMAppService.mainApp.status }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            loginStatus = SMAppService.mainApp.status
        }
        .padding(20)
        .frame(width: 520)
        .navigationTitle(app.t("settings.title"))
    }
}
