import SwiftUI
import AppKit
import DiscoKit

// Decrypted vault browser (presented as a sheet). Navigate by dirID, open files
// (download ciphertext from server → decrypt → open), upload, create folder.
struct VaultBrowserView: View {
    @EnvironmentObject var app: AppState
    let session: AppState.VaultSession

    @State private var stack: [(dirID: String, name: String)] = [("", "")]
    @State private var entries: [VaultEntry] = []
    @State private var loading = false
    @State private var busy = false
    @State private var error: String?
    @State private var newFolderPresented = false
    @State private var newFolderName = ""
    @State private var renameTarget: VaultEntry?
    @State private var renameName = ""

    private var currentDirID: String { stack.last!.dirID }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Button { if stack.count > 1 { stack.removeLast(); Task { await load() } } }
                    label: { Image(systemName: "chevron.left") }
                    .disabled(stack.count <= 1)
                Image(systemName: "lock.fill").foregroundStyle(.orange)
                Text(session.name + stack.dropFirst().map { " / " + $0.name }.joined())
                    .font(.headline).lineLimit(1)
                Spacer()
                if busy { ProgressView().controlSize(.small) }
                Button { pickAndUpload() } label: { Image(systemName: "plus") }
                    .help(app.t("vault.addFile"))
                Button { newFolderName = ""; newFolderPresented = true } label: { Image(systemName: "folder.badge.plus") }
                    .help(app.t("toolbar.newFolder"))
                Button(app.t("vault.close")) { app.closeVault() }
            }
            .padding(10)
            Divider()

            if loading {
                Spacer(); ProgressView(); Spacer()
            } else if entries.isEmpty {
                Spacer(); Text(app.t("browse.empty")).foregroundStyle(.secondary); Spacer()
            } else {
                List(entries, id: \.name) { e in
                    HStack {
                        Image(systemName: e.isDir ? "folder" : "doc")
                        Text(e.name)
                        Spacer()
                    }
                    .contentShape(Rectangle())
                    .onTapGesture(count: 2) {
                        if e.isDir, let id = e.dirID { stack.append((id, e.name)); Task { await load() } }
                        else { Task { await openFile(e) } }
                    }
                    .contextMenu {
                        Button(app.t("menu.rename")) { renameName = e.name; renameTarget = e }
                        Button(app.t("menu.delete"), role: .destructive) { Task { await deleteEntry(e) } }
                    }
                }
            }
            if let error { Text(error).foregroundStyle(.red).font(.caption).padding(6) }
        }
        .frame(minWidth: 620, minHeight: 440)
        .task { await load() }
        .alert(app.t("toolbar.newFolder"), isPresented: $newFolderPresented) {
            TextField(app.t("dialog.folderName"), text: $newFolderName)
            Button(app.t("dialog.create")) { let n = newFolderName; Task { await makeFolder(n) } }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
        .alert(app.t("menu.rename"), isPresented: Binding(
            get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })) {
            TextField(app.t("dialog.newName"), text: $renameName)
            Button(app.t("menu.rename")) {
                if let e = renameTarget { let nm = renameName; Task { await renameEntry(e, nm) } }
            }
            Button(app.t("dialog.cancel"), role: .cancel) {}
        }
    }

    private func deleteEntry(_ e: VaultEntry) async {
        busy = true; error = nil
        do { try await session.vault.deleteEntry(e, source: session.io, sink: session.io) }
        catch { self.error = error.localizedDescription }
        await app.refresh(); await load(); busy = false
    }

    private func renameEntry(_ e: VaultEntry, _ newName: String) async {
        guard !newName.isEmpty, newName != e.name else { return }
        busy = true; error = nil
        do { try await session.vault.renameEntry(e, to: newName, parentDirID: currentDirID, source: session.io, sink: session.io) }
        catch { self.error = error.localizedDescription }
        await app.refresh(); await load(); busy = false
    }

    private func load() async {
        loading = true; error = nil
        do {
            let list = try await session.vault.listEntries(dirID: currentDirID, source: session.io)
            entries = list.sorted { ($0.isDir ? 0 : 1, $0.name) < ($1.isDir ? 0 : 1, $1.name) }
        } catch { self.error = error.localizedDescription }
        loading = false
    }

    private func openFile(_ e: VaultEntry) async {
        guard let cp = e.contentPath else { return }
        busy = true; error = nil
        do {
            let data = try await session.vault.decryptFile(at: cp, source: session.io)
            let tmp = FileManager.default.temporaryDirectory
                .appendingPathComponent(UUID().uuidString, isDirectory: true)
            try FileManager.default.createDirectory(at: tmp, withIntermediateDirectories: true)
            let url = tmp.appendingPathComponent(e.name)
            try data.write(to: url)
            // The decrypted name is whatever the vault's writer chose; opened like a download.
            DownloadQuarantine.mark(url)
            NSWorkspace.shared.open(url)
        } catch { self.error = error.localizedDescription }
        busy = false
    }

    private func pickAndUpload() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        guard panel.runModal() == .OK else { return }
        let urls = panel.urls, dirID = currentDirID
        Task {
            busy = true; error = nil
            for url in urls where url.startAccessingSecurityScopedResource() {
                defer { url.stopAccessingSecurityScopedResource() }
                if let data = try? Data(contentsOf: url) {
                    do { try await session.vault.addFile(name: url.lastPathComponent, data: data, parentDirID: dirID, sink: session.io) }
                    catch { self.error = error.localizedDescription }
                }
            }
            await app.refresh()   // update the index so listEntries sees the new entries
            await load()
            busy = false
        }
    }

    private func makeFolder(_ name: String) async {
        guard !name.isEmpty else { return }
        busy = true; error = nil
        let dirID = currentDirID
        do { try await session.vault.createFolder(name: name, parentDirID: dirID, sink: session.io) }
        catch { self.error = error.localizedDescription }
        await app.refresh()
        await load()
        busy = false
    }
}
