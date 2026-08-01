import SwiftUI
import DiscoKit

struct SettingsView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                Picker(app.t("settings.language"), selection: Binding(
                    get: { app.language }, set: { l in Task { await app.setLanguage(l) } })) {
                    ForEach(L10n.supported, id: \.self) { Text(L10n.displayName[$0] ?? $0).tag($0) }
                }
                NavigationLink(app.t("au.title")) { AutoUploadView() }
            }
            .navigationTitle(app.t("settings.title"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .topBarTrailing) { Button(app.t("dialog.done")) { dismiss() } } }
        }
    }
}
