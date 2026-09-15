import SwiftUI
import AppKit
import DiscoKit

// Sentinel tag for the root: used inside OutlineGroup as a plain string → native selection,
// maps to a nil parent.
private let kRootTag = "__root__"

// Unified sidebar model: root + folder tree in a single OutlineGroup for
// consistent native selection behavior.
private struct SidebarNode: Identifiable {
    let id: String
    let name: String
    let icon: String
    var children: [SidebarNode]?
}

struct BrowserView: View {
    @EnvironmentObject var app: AppState
    @Environment(\.scenePhase) private var scenePhase
    @State private var selectedFolder: String? = kRootTag   // kRootTag = root
    @State private var selectedFile: Node?
    @State private var newFolderPresented = false
    @State private var newFolderName = ""
    @State private var renameTarget: Node?
    @State private var renameName = ""
    @State private var createVaultPresented = false
    @State private var logoutConfirmPresented = false

    static let sidebarLogo: NSImage = {
        let img = NSImage(named: "DDLogo") ?? NSImage()
        img.isTemplate = true
        return img
    }()

    // The selected folder and its ancestors, top down; empty at the root.
    private func folderChain() -> [Node] {
        guard let sel = selectedFolder, sel != kRootTag else { return [] }
        var chain: [Node] = []
        var id: String? = sel
        while let cur = id, let node = app.node(id: cur), chain.count < 64 {
            chain.insert(node, at: 0)
            id = node.parentID
        }
        return chain
    }

    // Path of the currently selected folder (root → "").
    private var currentFolderPath: String {
        guard let sel = selectedFolder, sel != kRootTag else { return "" }
        return app.node(id: sel)?.path ?? ""
    }

    private func pickFiles() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        guard panel.runModal() == .OK else { return }
        let urls = panel.urls, path = currentFolderPath
        Task { await app.upload(urls, toFolderPath: path) }
    }

    private func sidebarModel() -> [SidebarNode] {
        func map(_ items: [AppState.FolderItem]) -> [SidebarNode] {
            items.map { SidebarNode(id: $0.node.id, name: $0.node.name, icon: "folder",
                                    children: $0.children.map(map)) }
        }
        return [SidebarNode(id: kRootTag, name: "DiscoDrive", icon: "DDLogo", children: nil)]
            + map(app.tree)
    }

    var body: some View {
        NavigationSplitView {
            List(selection: $selectedFolder) {
                OutlineGroup(sidebarModel(), children: \.children) { item in
                    // The root wears the logo (a custom symbol from Assets); folders a system one.
                    Label { Text(item.name) } icon: {
                        // The sidebar tints template NSImages like its own symbols; a custom
                        // symbol image loses its colour there once the row is not selected.
                        item.id == kRootTag ? Image(nsImage: Self.sidebarLogo) : Image(systemName: item.icon)
                    }.tag(Optional(item.id))
                }
            }
            .listStyle(.sidebar)
        } detail: {
            let files = app.children(of: selectedFolder == kRootTag ? nil : selectedFolder)
            VStack(alignment: .leading, spacing: 0) {
                // Failures surface here — a refresh that could not reach the server, an upload
                // that was refused — instead of vanishing into a status line nobody shows.
                if let error = app.lastError {
                    HStack(spacing: 8) {
                        Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        Text(error).font(.callout).lineLimit(2).textSelection(.enabled)
                        Spacer()
                        Button { app.lastError = nil } label: { Image(systemName: "xmark") }
                            .buttonStyle(.borderless).help(app.t("dialog.cancel"))
                    }
                    .padding(8)
                    .background(Color.orange.opacity(0.12))
                    Divider()
                }
                // Where we are, as a path one can climb back up one step at a time; the
                // sidebar alone only offers the root once the tree is folded away.
                HStack(spacing: 2) {
                    let chain = folderChain()
                    Button { selectedFolder = kRootTag; selectedFile = nil } label: {
                        Image("DDLogo").renderingMode(.template)
                    }
                    .buttonStyle(.borderless).disabled(chain.isEmpty)
                    ForEach(chain) { folder in
                        Image(systemName: "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                        Button { selectedFolder = folder.id; selectedFile = nil } label: {
                            Text(folder.name).lineLimit(1)
                        }
                        .buttonStyle(.borderless)
                        .disabled(folder.id == chain.last?.id)
                        .fontWeight(folder.id == chain.last?.id ? .semibold : .regular)
                    }
                    Spacer()
                    Text("\(app.t("browse.count")): \(files.count)")
                        .font(.caption).foregroundStyle(.secondary)
                }
                .font(.callout).padding(.horizontal, 8).padding(.vertical, 5)
                Divider()
                if files.isEmpty {
                    Spacer()
                    Text(app.t("browse.empty"))
                        .foregroundStyle(.secondary).frame(maxWidth: .infinity)
                    Spacer()
                } else {
                    List(files, selection: $selectedFile) { node in
                        HStack(spacing: 4) {
                            FileRow(node: node, status: app.status(of: node))
                            if node.isDir && app.isVault(node) {
                                Image(systemName: "lock.fill").foregroundStyle(.orange)
                            }
                        }
                            .tag(node)
                            .contentShape(Rectangle())
                            .contextMenu { rowMenu(node) }
                            // count:2 before count:1 — makes mouse navigation deterministic
                            .onTapGesture(count: 2) {
                                if node.isDir {
                                    if app.isVault(node) { app.vaultUnlockFolder = node }
                                    else { selectedFolder = node.id; selectedFile = nil }
                                } else { Task { await app.openFile(node) } }
                            }
                            .onTapGesture { selectedFile = node }
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            // Drag-and-drop files → upload to the current folder.
            .dropDestination(for: URL.self) { urls, _ in
                let p = currentFolderPath
                Task { await app.upload(urls, toFolderPath: p) }
                return true
            }
        }
        .toolbar {
            Button { Task { await app.refresh() } } label: { Image(systemName: "arrow.clockwise") }
                .help(app.t("toolbar.refresh"))
            Button { Task { await app.importLocalFiles() } } label: { Image(systemName: "square.and.arrow.down.on.square") }
                .help(app.t("toolbar.import"))
            Button { newFolderName = ""; newFolderPresented = true } label: { Image(systemName: "folder.badge.plus") }
                .help(app.t("toolbar.newFolder"))
            Button { pickFiles() } label: { Image(systemName: "plus") }
                .help(app.t("toolbar.addFile"))
            Button { createVaultPresented = true } label: { Image(systemName: "lock.rectangle") }
                .help(app.t("vault.createVault"))
            Button { app.openLocalFolderInFinder() } label: { Image(systemName: "folder") }
                .help(app.t("toolbar.openFinder"))
            Button { try? app.local?.evictCached() } label: { Image(systemName: "trash") }
                .help(app.t("toolbar.free"))
            Button { logoutConfirmPresented = true } label: { Image(systemName: "rectangle.portrait.and.arrow.right") }
                .help(app.t("toolbar.logout"))
        }
        .task { await app.refresh(); await app.importLocalFiles() }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { Task { await app.importLocalFiles() } }
        }
        .alert(app.t("toolbar.newFolder"), isPresented: $newFolderPresented) {
            TextField(app.t("dialog.folderName"), text: $newFolderName)
            Button(app.t("dialog.create")) {
                let name = newFolderName, path = currentFolderPath
                Task { await app.createFolder(name: name, inFolderPath: path) }
            }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
        .alert(app.t("menu.rename"), isPresented: Binding(
            get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })) {
            TextField(app.t("dialog.newName"), text: $renameName)
            Button(app.t("menu.rename")) {
                if let n = renameTarget { let nm = renameName; Task { await app.renameNode(n, to: nm) } }
            }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
        .alert(app.t("dialog.logoutTitle"), isPresented: $logoutConfirmPresented) {
            Button(app.t("toolbar.logout"), role: .destructive) { app.logout() }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        } message: {
            Text(app.t("dialog.logoutMessage"))
        }
        // Vault unlock sheet.
        .sheet(item: Binding(get: { app.vaultUnlockFolder },
                             set: { app.vaultUnlockFolder = $0; if $0 == nil { app.vaultUnlockError = nil } })) { folder in
            UnlockVaultView(folder: folder).environmentObject(app)
        }
        // Vault creation sheet.
        .sheet(isPresented: $createVaultPresented) {
            CreateVaultView(parentPath: currentFolderPath, isPresented: $createVaultPresented).environmentObject(app)
        }
        // Opened vault browser.
        .sheet(isPresented: Binding(get: { app.vaultSession != nil }, set: { if !$0 { app.closeVault() } })) {
            if let s = app.vaultSession { VaultBrowserView(session: s).environmentObject(app) }
        }
        // New vault recovery key sheet.
        .sheet(isPresented: Binding(get: { app.vaultRecoveryToShow != nil }, set: { if !$0 { app.vaultRecoveryToShow = nil } })) {
            if let p = app.vaultRecoveryToShow { RecoveryKeyView(phrase: p).environmentObject(app) }
        }
    }

    @ViewBuilder private func rowMenu(_ node: Node) -> some View {
        if node.isDir && app.isVault(node) {
            Button(app.t("vault.open")) { app.vaultUnlockFolder = node }
            Divider()
        }
        if !node.isDir {
            Button(app.t("menu.download")) { Task { await app.ensureDownloaded(node) } }
            Button(app.t("menu.keepLocal")) { Task { await app.pin(node) } }
            Button(app.t("menu.removeLocal")) { app.removeLocal(node) }
            Divider()
        }
        Button(app.t("menu.rename")) { renameName = node.name; renameTarget = node }
        Button(app.t("menu.delete"), role: .destructive) { Task { await app.deleteNode(node) } }
    }
}
