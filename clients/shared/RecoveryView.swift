import SwiftUI
import DiscoKit

// Recovery operates on server history; local copies are refreshed through the normal index.
struct RecoveryView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    var node: Node? = nil
    @State private var trash: [APIClient.TrashItem] = []
    @State private var versions: [APIClient.FileVersion] = []
    @State private var busy = false
    @State private var error: String?
    @State private var notice: String?
    @State private var pending: Action?

    private enum Action {
        case purge(String), empty, version(String, Int64)
    }
    private var title: String { app.t(node == nil ? "recovery.trash" : "recovery.versions") }

    var body: some View {
        NavigationStack {
            List {
                if let node { Text(node.name).font(.headline).textSelection(.enabled) }
                if busy { ProgressView().frame(maxWidth: .infinity).accessibilityLabel(app.t("fullSync.syncing")) }
                if let error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
                if let notice { Text(notice).foregroundStyle(.secondary) }
                if node == nil {
                    if trash.isEmpty && !busy && error == nil { Text(app.t("recovery.emptyTrash")).foregroundStyle(.secondary) }
                    ForEach(trash) { item in
                        VStack(alignment: .leading, spacing: 8) {
                            Label(item.name, systemImage: item.is_dir ? "folder" : "doc").textSelection(.enabled)
                            if let date = item.deleted_at { Text(serverDateLabel(date, language: app.language)).font(.caption).foregroundStyle(.secondary) }
                            HStack {
                                Button(app.t("recovery.restore")) { Task { await restore(item.id) } }
                                Spacer()
                                Button(app.t("recovery.purge"), role: .destructive) { pending = .purge(item.id) }
                            }.buttonStyle(.borderless).disabled(busy)
                        }.padding(.vertical, 4)
                    }
                } else {
                    Text(app.t("recovery.versionHint")).font(.callout).foregroundStyle(.secondary)
                    if versions.isEmpty && !busy && error == nil { Text(app.t("recovery.emptyVersions")).foregroundStyle(.secondary) }
                    ForEach(versions) { version in
                        VStack(alignment: .leading, spacing: 8) {
                            Text("\(app.t("recovery.version")) \(version.version)").font(.headline)
                            if let size = version.size { Text(ByteCountFormatter.string(fromByteCount: size, countStyle: .file)).foregroundStyle(.secondary) }
                            if version.is_conflict_loser { Text(app.t("recovery.conflict")).font(.caption).foregroundStyle(.secondary) }
                            Button(app.t("recovery.restore")) { if let node { pending = .version(node.id, version.version) } }
                                .buttonStyle(.borderless).disabled(busy)
                        }.padding(.vertical, 4)
                    }
                }
            }
            .navigationTitle(title)
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(app.t("dialog.done")) { dismiss() }.disabled(busy)
                }
                ToolbarItem(placement: .primaryAction) {
                    Button { Task { await reload() } } label: { Image(systemName: "arrow.clockwise") }
                        .accessibilityLabel(app.t("toolbar.refresh")).disabled(busy)
                }
                if node == nil {
                    #if os(iOS)
                    ToolbarItem(placement: .bottomBar) { emptyButton }
                    #else
                    ToolbarItem(placement: .automatic) { emptyButton }
                    #endif
                }
            }
        }
        #if os(macOS)
        .frame(width: 560, height: 540)
        #endif
        .interactiveDismissDisabled(busy)
        .task { await reload() }
        .onChange(of: app.paired) { _, _ in dismiss() }
        .alert(app.t(node == nil ? "recovery.purge" : "recovery.restore"), isPresented: Binding(
            get: { pending != nil }, set: { if !$0 { pending = nil } })) {
            Button(app.t(node == nil ? "recovery.purge" : "recovery.restore"), role: .destructive) {
                if let action = pending { Task { await perform(action) } }
            }
            Button(app.t("dialog.cancel"), role: .cancel) { pending = nil }
        } message: {
            Text(confirmationName + "\n\n" + app.t(node == nil ? "recovery.purgeWarning" : "recovery.versionHint"))
        }
    }

    private var emptyButton: some View {
        Button(app.t("recovery.empty"), role: .destructive) { pending = .empty }
            .disabled(busy || trash.isEmpty)
    }

    private var confirmationName: String {
        switch pending {
        case .purge(let id): return trash.first { $0.id == id }?.name ?? ""
        case .empty: return app.t("recovery.empty")
        case .version(_, let version): return "\(node?.name ?? "") — \(app.t("recovery.version")) \(version)"
        case nil: return ""
        }
    }

    private func load() async throws {
        if let node {
            versions = try await app.performAccountOperation { try await $0.versions(nodeID: node.id) }
                .sorted { $0.version > $1.version }
        } else { trash = try await app.performAccountOperation { try await $0.trash() } }
    }
    private func reload() async {
        guard !busy else { return }
        busy = true; error = nil; defer { busy = false }
        do { try await load() } catch { show(error) }
    }
    private func restore(_ id: String) async {
        guard !busy else { return }
        busy = true; error = nil; notice = nil; defer { busy = false }
        do {
            try await app.performAccountOperation { try await $0.undelete(id: id) }
            notice = app.t("recovery.restored")
            try await load()
            _ = await app.refresh()
        } catch { show(error) }
    }
    private func perform(_ action: Action) async {
        guard !busy else { return }
        busy = true; pending = nil; error = nil; notice = nil; defer { busy = false }
        do {
            switch action {
            case .purge(let id): try await app.performAccountOperation { try await $0.purge(id: id) }
            case .empty: try await app.performAccountOperation { try await $0.emptyTrash() }
            case .version(let id, let version):
                try await app.performAccountOperation { try await $0.restoreVersion(nodeID: id, version: version) }
                notice = app.t("recovery.restored")
            }
            try await load()
            _ = await app.refresh()
        } catch { show(error) }
    }
    private func show(_ error: Error) { self.error = app.userMessage(for: error) ?? app.t("status.opError") }
}
