import SwiftUI
import DiscoKit
import Photos

/// The auto-upload screen: one switch, where it goes, when it may run, and what happened.
///
/// It is deliberately honest about the two things iOS cannot promise — background timing is
/// the system's call, and photos are only ever copied.
struct AutoUploadView: View {
    @EnvironmentObject var app: AppState
    @ObservedObject private var service = AutoUploadService.shared

    @State private var enabled = AutoUploadSettings.shared.enabled
    @State private var wifiOnly = AutoUploadSettings.shared.wifiOnly
    @State private var chargingOnly = AutoUploadSettings.shared.chargingOnly
    @State private var requireBattery = AutoUploadSettings.shared.requireBattery
    @State private var counts = JournalCounts(sent: 0, skipped: 0, deferred: 0)
    @State private var showLog = false
    @State private var accessDenied = false

    private let settings = AutoUploadSettings.shared

    var body: some View {
        Form {
            Section {
                Toggle(app.t("au.master"), isOn: $enabled)
                    .onChange(of: enabled) { _, on in
                        Task {
                            await service.setEnabled(on)
                            enabled = settings.enabled
                            accessDenied = on && !settings.enabled
                            refresh()
                        }
                    }
                if accessDenied {
                    Text(app.t("au.noAccess")).foregroundStyle(.red).font(.footnote)
                }
                Text(app.t("au.neverDeletes")).font(.footnote).foregroundStyle(.secondary)
            }

            Section(app.t("au.destination")) {
                Text("/\(AutoUploadRunner.destRoot)/\(AutoUploadRunner.deviceFolder)")
                    .font(.footnote).foregroundStyle(.secondary)
            }

            Section(app.t("au.when")) {
                Toggle(app.t("au.wifi"), isOn: $wifiOnly)
                    .onChange(of: wifiOnly) { _, v in settings.wifiOnly = v }
                Toggle(app.t("au.charging"), isOn: $chargingOnly)
                    .onChange(of: chargingOnly) { _, v in settings.chargingOnly = v }
                Toggle(app.t("au.battery"), isOn: $requireBattery)
                    .onChange(of: requireBattery) { _, v in settings.requireBattery = v }
            }

            Section {
                if let text = service.progressText {
                    HStack { ProgressView(); Text(text).font(.footnote) }
                } else if let blocked = blockedText {
                    Text(blocked).font(.footnote).foregroundStyle(.orange)
                } else if let failure = service.lastResult?.error {
                    // Without this a failed pass looks exactly like an idle one: counters at
                    // zero and no hint of why.
                    Text(failure).font(.footnote).foregroundStyle(.red)
                }
                Text(app.t("au.stats")
                    .replacingOccurrences(of: "%1", with: "\(counts.sent)")
                    .replacingOccurrences(of: "%2", with: "\(counts.skipped)")
                    .replacingOccurrences(of: "%3", with: "\(counts.deferred)"))
                    .font(.footnote).foregroundStyle(.secondary)
                Button(app.t("au.uploadNow")) {
                    Task { await service.runPass(); refresh() }
                }
                .disabled(!enabled || service.running)
                Button(app.t("au.log")) { showLog = true }
            } footer: {
                Text(app.t("au.background")).font(.footnote)
            }
        }
        .navigationTitle(app.t("au.title"))
        .navigationBarTitleDisplayMode(.inline)
        .onAppear { refresh() }
        .sheet(isPresented: $showLog) { LogView(entries: logEntries()) }
    }

    private var blockedText: String? {
        guard enabled else { return nil }
        switch service.lastResult?.blocked ?? .none {
        case .none: return nil
        case .noNetwork: return app.t("au.blocked.network")
        case .needsWiFi: return app.t("au.blocked.wifi")
        case .notCharging: return app.t("au.blocked.charging")
        case .lowBattery: return app.t("au.blocked.battery")
        }
    }

    private func refresh() {
        counts = (try? service.openJournal().counts()) ?? JournalCounts(sent: 0, skipped: 0, deferred: 0)
    }

    private func logEntries() -> [JournalEntry] {
        (try? service.openJournal().recent(limit: 200)) ?? []
    }
}

private struct LogView: View {
    let entries: [JournalEntry]
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List(entries, id: \.assetID) { e in
                VStack(alignment: .leading, spacing: 2) {
                    Text(e.serverName.isEmpty ? e.assetID : e.serverName)
                    Text(e.error.map { "\(e.state.rawValue) · \($0)" } ?? e.state.rawValue)
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .overlay {
                if entries.isEmpty { Text(app.t("au.emptyLog")).foregroundStyle(.secondary) }
            }
            .navigationTitle(app.t("au.log"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button(app.t("dialog.done")) { dismiss() }
                }
            }
        }
    }
}
