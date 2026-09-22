import SwiftUI

struct SyncActivity: Decodable {
    var phase = ""
    var path = ""
    var completed: Int64 = 0
    var errors: [Failure] = []
    struct Failure: Decodable {
        let path: String
        let message: String
        let time: String
    }
}

struct SyncActivityView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var fullSync: FullSyncController
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Text(app.t(fullSync.statusKey))
                    if !fullSync.activity.phase.isEmpty {
                        HStack { ProgressView(); Text(app.t("activity." + fullSync.activity.phase)) }
                        if !fullSync.activity.path.isEmpty { Text(fullSync.activity.path).textSelection(.enabled) }
                    }
                    LabeledContent(app.t("activity.completed"), value: String(fullSync.activity.completed))
                    if fullSync.activity.completed == 0 && fullSync.activity.phase.isEmpty && fullSync.activity.errors.isEmpty {
                        Text(app.t("activity.empty")).foregroundStyle(.secondary)
                    }
                } footer: { Text(app.t("activity.hint")) }
                if !fullSync.activityError.isEmpty {
                    Section { Text(fullSync.activityError).foregroundStyle(.red).textSelection(.enabled) }
                }
                if !fullSync.activity.errors.isEmpty {
                    Section {
                        ForEach(Array(fullSync.activity.errors.reversed().enumerated()), id: \.offset) { _, failure in
                            VStack(alignment: .leading, spacing: 6) {
                                if !failure.path.isEmpty { Text(failure.path).font(.headline) }
                                Text(failure.message).foregroundStyle(.secondary)
                                Text(serverDateLabel(failure.time, language: app.language)).font(.caption).foregroundStyle(.secondary)
                            }.textSelection(.enabled)
                        }
                    } header: { Text(app.t("activity.errors")) }
                }
            }
            .navigationTitle(app.t("activity.title"))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button(app.t("dialog.done")) { dismiss() } } }
        }
        #if os(macOS)
        .frame(width: 560, height: 500)
        #endif
    }
}

func serverDateLabel(_ raw: String, language: String) -> String {
    let parser = ISO8601DateFormatter()
    parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    var date = parser.date(from: raw)
    if date == nil { parser.formatOptions = [.withInternetDateTime]; date = parser.date(from: raw) }
    guard let date else { return raw }
    let formatter = DateFormatter()
    formatter.locale = Locale(identifier: language)
    formatter.dateStyle = .medium
    formatter.timeStyle = .short
    return formatter.string(from: date)
}
