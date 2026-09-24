import SwiftUI
import QuickLook
import DiscoKit

// Root: NavigationStack + global actions and sheets.
// URL wrapper with a fresh id on each presentation — so .sheet re-triggers even for the same file.
struct PreviewItem: Identifiable { let id = UUID(); let url: URL; let node: Node?; let siblings: [Node] }

struct PreviewFile: Identifiable { let id: String; let name: String }

// Controls live outside Quick Look so image gestures cannot hide them.
struct FilePreviewView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.dismiss) private var dismiss
    let files: [PreviewFile]
    let load: @MainActor (String) async throws -> URL
    @State private var position: Int
    @State private var url: URL?
    @State private var loading = false
    @State private var error: String?
    @State private var retry = 0
    @State private var exportURL: ExportFile?

    init(files: [PreviewFile], selectedID: String, url: URL,
         load: @escaping @MainActor (String) async throws -> URL) {
        self.files = files; self.load = load
        _position = State(initialValue: files.firstIndex { $0.id == selectedID } ?? 0)
        _url = State(initialValue: url)
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Button { navigate(-1) } label: { Image(systemName: "chevron.left").frame(minWidth: 44, minHeight: 44) }
                    .accessibilityLabel(app.t("preview.previous")).accessibilityIdentifier("preview.previous")
                    .disabled(position == 0)
                Button { navigate(1) } label: { Image(systemName: "chevron.right").frame(minWidth: 44, minHeight: 44) }
                    .accessibilityLabel(app.t("preview.next")).accessibilityIdentifier("preview.next")
                    .disabled(position + 1 >= files.count)
                Button {
                    if let url { exportURL = ExportFile(url: url) }
                } label: {
                    Image(systemName: "square.and.arrow.down").frame(minWidth: 44, minHeight: 44)
                }
                .accessibilityLabel(app.t("menu.saveToFiles"))
                .accessibilityIdentifier("preview.saveToFiles")
                .disabled(url == nil || loading)
                Spacer()
                Text("\(position + 1) / \(files.count)").monospacedDigit().foregroundStyle(.secondary)
                Button(app.t("preview.close")) { dismiss() }
                    .frame(minHeight: 44).accessibilityIdentifier("preview.close")
            }.padding(.horizontal)
            if files.indices.contains(position) {
                Text(files[position].name).font(.subheadline).lineLimit(1).truncationMode(.middle).padding(.horizontal)
            }
            if loading { Spacer(); ProgressView(); Spacer() }
            else if let error {
                Spacer()
                Text(error).foregroundStyle(.red).padding()
                Button(app.t("toolbar.refresh")) { retry += 1 }
                Spacer()
            } else if let url { QuickLookView(url: url).id(url) }
        }
        .background(Color(uiColor: .systemBackground))
        .sheet(item: $exportURL) { item in SaveToFilesView(url: item.url) }
        .task(id: "\(position):\(retry)") {
            guard url == nil, files.indices.contains(position) else { return }
            loading = true; error = nil
            do {
                let result = try await load(files[position].id)
                try Task.checkCancellation()
                url = result; loading = false
            } catch {
                guard !Task.isCancelled else { return }
                self.error = error.localizedDescription; loading = false
            }
        }
    }
    private func navigate(_ offset: Int) {
        url = nil; error = nil; loading = true; position += offset
    }
}

final class ExportFile: Identifiable {
    let id = UUID()
    let url: URL
    private let temporaryDirectory: URL?
    init(url: URL, temporaryDirectory: URL? = nil) {
        self.url = url; self.temporaryDirectory = temporaryDirectory
    }
    deinit {
        if let temporaryDirectory { try? FileManager.default.removeItem(at: temporaryDirectory) }
    }
}

// Export a copy through the system picker; the original stays managed by the app.
struct SaveToFilesView: UIViewControllerRepresentable {
    let url: URL
    @Environment(\.dismiss) private var dismiss

    func makeUIViewController(context: Context) -> UIDocumentPickerViewController {
        let picker = UIDocumentPickerViewController(forExporting: [url], asCopy: true)
        picker.delegate = context.coordinator
        return picker
    }
    func updateUIViewController(_ controller: UIDocumentPickerViewController, context: Context) {}
    func makeCoordinator() -> Coordinator { Coordinator { dismiss() } }

    final class Coordinator: NSObject, UIDocumentPickerDelegate {
        let finish: () -> Void
        init(finish: @escaping () -> Void) { self.finish = finish }
        func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) { finish() }
        func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) { finish() }
    }
}

// QuickLook via a UIKit controller: reliably opens any local file, including repeated opens.
struct QuickLookView: UIViewControllerRepresentable {
    let url: URL
    func makeUIViewController(context: Context) -> QLPreviewController {
        let c = QLPreviewController(); c.dataSource = context.coordinator; return c
    }
    func updateUIViewController(_ controller: QLPreviewController, context: Context) {}
    func makeCoordinator() -> Coordinator { Coordinator(url: url) }
    final class Coordinator: NSObject, QLPreviewControllerDataSource {
        let url: URL
        init(url: URL) { self.url = url }
        func numberOfPreviewItems(in controller: QLPreviewController) -> Int { 1 }
        func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> QLPreviewItem { url as NSURL }
    }
}

struct BrowserView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.scenePhase) private var scenePhase
    @State private var trashPresented = false
    @State private var settingsPresented = false
    @State private var preview: PreviewItem?

    var body: some View {
        NavigationStack {
            FolderView(folder: nil)
                .navigationDestination(for: Node.self) { node in FolderView(folder: node) }
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Menu {
                            Button { Task { await app.refresh() } } label: { Label(app.t("toolbar.refresh"), systemImage: "arrow.clockwise") }
                            Button { trashPresented = true } label: { Label(app.t("recovery.trash"), systemImage: "trash.circle") }
                            Button { settingsPresented = true } label: { Label(app.t("settings.title"), systemImage: "gear") }
                            Button { try? app.local?.evictCached() } label: { Label(app.t("toolbar.free"), systemImage: "trash") }
                            Divider()
                            Button(role: .destructive) { app.logout() } label: { Label(app.t("toolbar.logout"), systemImage: "rectangle.portrait.and.arrow.right") }
                        } label: { Image(systemName: "ellipsis.circle") }.accessibilityIdentifier("browser.actions")
                    }
                }
        }
        .alert(app.t("status.opError"), isPresented: Binding(get: { app.lastError != nil }, set: { if !$0 { app.lastError = nil } })) {
            Button(app.t("dialog.done")) { app.lastError = nil }
        } message: { Text(app.lastError ?? "") }
        .task { await app.refresh() }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { Task { await app.importLocalFiles() } }
        }
        .sheet(isPresented: $trashPresented) { RecoveryView() }
        .sheet(isPresented: $settingsPresented) { SettingsView() }
        .sheet(item: Binding(get: { app.vaultUnlockFolder },
                             set: { app.vaultUnlockFolder = $0; if $0 == nil { app.vaultUnlockError = nil } })) { folder in
            UnlockVaultView(folder: folder).environmentObject(app)
        }
        .sheet(isPresented: Binding(get: { app.vaultSession != nil }, set: { if !$0 { app.closeVault() } })) {
            if let s = app.vaultSession { VaultBrowserView(session: s).environmentObject(app) }
        }
        .sheet(isPresented: Binding(get: { app.vaultRecoveryToShow != nil }, set: { if !$0 { app.vaultRecoveryToShow = nil } })) {
            if let p = app.vaultRecoveryToShow { RecoveryKeyView(phrase: p).environmentObject(app) }
        }
        .onChange(of: app.fileToPreview) { _, url in
            if let url {
                let node = app.previewNode
                preview = PreviewItem(url: url, node: node, siblings: node.map { app.children(of: $0.parentID).filter { !$0.isDir } } ?? [])
                app.fileToPreview = nil
            }
        }
        .sheet(item: $preview) { item in
            let nodes = item.siblings.isEmpty ? item.node.map { [$0] } ?? [] : item.siblings
            let files = nodes.isEmpty ? [PreviewFile(id: "local", name: item.url.lastPathComponent)] : nodes.map { PreviewFile(id: $0.id, name: $0.name) }
            FilePreviewView(files: files, selectedID: item.node?.id ?? "local", url: item.url) { id in
                guard let node = nodes.first(where: { $0.id == id }), let url = await app.ensureDownloaded(node) else { throw CocoaError(.fileReadUnknown) }
                return url
            }
        }
    }
}

// Contents of a single folder (nil = root).
struct FolderView: View {
    @EnvironmentObject var app: AppState
    let folder: Node?
    @State private var newFolderPresented = false
    @State private var newFolderName = ""
    @State private var createVaultPresented = false
    @State private var importing = false
    @State private var renameTarget: Node?
    @State private var renameName = ""
    @State private var historyTarget: Node?
    @State private var shareTarget: Node?
    @State private var exportURL: ExportFile?
    @State private var preparingExport = false

    private var folderID: String? { folder?.id }
    private var folderPath: String { folder?.path ?? "" }

    var body: some View {
        List {
            ForEach(app.children(of: folderID)) { node in row(node) }
        }
        .overlay {
            if app.children(of: folderID).isEmpty {
                VStack(spacing: 12) {
                    if app.fileListLoading { ProgressView(app.t("browse.loading")) }
                    else if let error = app.fileListError {
                        Text(error).foregroundStyle(.secondary)
                        Button(app.t("toolbar.refresh")) { Task { await app.refresh() } }
                    } else { Text(app.t("browse.empty")).foregroundStyle(.secondary) }
                }.padding()
            }
        }
        .navigationTitle(folder?.name ?? "DiscoDrive")
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await app.refresh(); await app.importLocalFiles() }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button { newFolderName = ""; newFolderPresented = true } label: { Label(app.t("toolbar.newFolder"), systemImage: "folder.badge.plus") }
                    Button { importing = true } label: { Label(app.t("toolbar.addFile"), systemImage: "doc.badge.plus") }
                    Button { createVaultPresented = true } label: { Label(app.t("vault.createVault"), systemImage: "lock.rectangle") }
                } label: { Image(systemName: "plus") }
            }
        }
        .alert(app.t("toolbar.newFolder"), isPresented: $newFolderPresented) {
            TextField(app.t("dialog.folderName"), text: $newFolderName)
            Button(app.t("dialog.create")) { let n = newFolderName, p = folderPath; Task { await app.createFolder(name: n, inFolderPath: p) } }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
        .alert(app.t("menu.rename"), isPresented: Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })) {
            TextField(app.t("dialog.newName"), text: $renameName)
            Button(app.t("menu.rename")) { if let n = renameTarget { let nm = renameName; Task { await app.renameNode(n, to: nm) } } }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
        .sheet(isPresented: $createVaultPresented) {
            CreateVaultView(parentPath: folderPath, isPresented: $createVaultPresented).environmentObject(app)
        }
        .sheet(item: $historyTarget) { RecoveryView(node: $0) }
        .sheet(item: $shareTarget) { SharingView(node: $0).environmentObject(app) }
        .sheet(item: $exportURL) { SaveToFilesView(url: $0.url) }
        .overlay { if preparingExport { ProgressView().padding().background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12)) } }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            guard case .success(let urls) = result else { return }
            let accessed = urls.filter { $0.startAccessingSecurityScopedResource() }
            let p = folderPath
            Task {
                await app.upload(accessed, toFolderPath: p)
                accessed.forEach { $0.stopAccessingSecurityScopedResource() }
            }
        }
    }

    @ViewBuilder private func row(_ node: Node) -> some View {
        if node.isDir {
            if app.isVault(node) {
                Button { app.vaultUnlockFolder = node } label: {
                    Label(node.name, systemImage: "lock.fill").foregroundStyle(.primary)
                }
            } else {
                NavigationLink(value: node) { Label(node.name, systemImage: "folder") }
                    .contextMenu { Button(app.t("share.title")) { shareTarget = node } }
            }
        } else {
            Button { Task { await app.openFile(node) } } label: {
                HStack {
                    Image(systemName: "doc")
                    Text(node.name).foregroundStyle(.primary).lineLimit(1)
                    Spacer()
                    Text(ByteCountFormatter.string(fromByteCount: node.size, countStyle: .file))
                        .font(.caption).foregroundStyle(.secondary)
                    if app.isDownloading(node) {
                        ProgressView()
                    } else {
                        statusIcon(app.status(of: node))
                    }
                }
            }
            .swipeActions(edge: .trailing) {
                Button(role: .destructive) { Task { await app.deleteNode(node) } } label: { Label(app.t("menu.delete"), systemImage: "trash") }
                Button { renameName = node.name; renameTarget = node } label: { Label(app.t("menu.rename"), systemImage: "pencil") }.tint(.blue)
            }
            .contextMenu {
                Button(app.t("recovery.versions")) { historyTarget = node }
                Button(app.t("share.title")) { shareTarget = node }
                Button {
                    preparingExport = true
                    Task {
                        defer { preparingExport = false }
                        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("Exports/" + UUID().uuidString)
                        if let url = await app.prepareExport(node, in: directory) {
                            exportURL = ExportFile(url: url, temporaryDirectory: directory)
                        }
                    }
                } label: { Label(app.t("menu.saveToFiles"), systemImage: "square.and.arrow.down") }
                .disabled(preparingExport)
                Button(app.t("ios.keepInApp")) { Task { await app.pin(node) } }
                Button(app.t("ios.removeFromApp")) { app.removeLocal(node) }
            }
        }
    }
}

@ViewBuilder func statusIcon(_ s: LocalStatus) -> some View {
    switch s {
    case .none:   Image(systemName: "icloud").foregroundStyle(.secondary)
    case .cached: Image(systemName: "checkmark.circle").foregroundStyle(.blue)
    case .pinned: Image(systemName: "pin.fill").foregroundStyle(.orange)
    case .stale:  Image(systemName: "exclamationmark.icloud").foregroundStyle(.yellow)
    }
}
