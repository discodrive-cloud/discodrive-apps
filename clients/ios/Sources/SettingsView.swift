import SwiftUI
import DiscoKit

struct SettingsView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var files: FilesIntegration
    @EnvironmentObject var fullSync: FullSyncController
    @Environment(\.dismiss) private var dismiss
    @State private var confirmingDeletion = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker(app.t("settings.language"), selection: Binding(
                        get: { app.language }, set: { l in Task { await app.setLanguage(l) } })) {
                        ForEach(L10n.supported, id: \.self) { Text(L10n.displayName[$0] ?? $0).tag($0) }
                    }
                    NavigationLink(app.t("au.title")) { AutoUploadView() }
                }
                Section {
                    Label(app.t(files.connected ? "ios.files.connected" : "ios.files.disconnected"), systemImage: "folder")
                    Button(app.t("ios.files.connect")) { Task { await files.connect() } }
                        .disabled(files.busy || !app.paired)
                    if files.openVaultCount > 0 {
                        Button(app.t("ios.files.lockAll")) { Task { await files.lockAll() } }
                            .disabled(files.busy)
                    }
                } header: { Text(app.t("ios.files.title")) }
                  footer: { Text(app.t("ios.files.hint")) }
                Section {
                    Toggle(app.t("fullSync.enable"), isOn: Binding(get: { fullSync.enabled }, set: { value in
                        Task { await fullSync.setEnabled(value) }
                    })).disabled(fullSync.busy || !app.paired)
                    LabeledContent(app.t("ios.sync.folder"), value: "DiscoDrive / Sync")
                    if fullSync.enabled || fullSync.busy || fullSync.statusKey != "fullSync.stopped" {
                        HStack {
                            if fullSync.busy || fullSync.statusKey == "fullSync.syncing" { ProgressView() }
                            Text(app.t(fullSync.statusKey)).foregroundStyle(.secondary)
                        }
                    }
                    if fullSync.statusKey == "fullSync.bulkDelete" {
                        Button(app.t("fullSync.confirmDelete"), role: .destructive) { confirmingDeletion = true }
                    }
                } header: { Text(app.t("fullSync.title")) }
                  footer: { Text(app.t("ios.sync.hint")) }
                Section {
                    Toggle(app.t("diagnostics.enable"), isOn: Binding(get: { fullSync.loggingEnabled }, set: { fullSync.setLogging($0) }))
                    if fullSync.loggingEnabled || FileManager.default.fileExists(atPath: fullSync.logURL.path) {
                        ShareLink(item: fullSync.logURL) { Label(app.t("diagnostics.export"), systemImage: "square.and.arrow.up") }
                    }
                } header: { Text(app.t("diagnostics.title")) }
                  footer: { Text(app.t("diagnostics.hint")) }
            }
            .navigationTitle(app.t("settings.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .topBarTrailing) { Button(app.t("dialog.done")) { dismiss() } } }
            .alert(app.t("fullSync.confirmDelete"), isPresented: $confirmingDeletion) {
                Button(app.t("fullSync.confirmDelete"), role: .destructive) { fullSync.confirmDeletion() }
                Button(app.t("dialog.cancel"), role: .cancel) {}
            } message: { Text(app.t("fullSync.deleteWarning")) }
        }
    }
}
