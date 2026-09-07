import SwiftUI
import UIKit

struct SyncView: View {
    @EnvironmentObject var model: AppModel
    @State private var confirmUnpair = false

    var body: some View {
        VStack(spacing: 20) {
            Image(systemName: "folder.badge.gearshape").font(.system(size: 44)).foregroundStyle(.tint)
            Text("DiscoDrive Fast Sync").font(.headline)
            Text(model.syncDirURL.path).font(.caption).foregroundStyle(.secondary)
                .multilineTextAlignment(.center).lineLimit(2)

            Button {
                Task { await model.syncNow() }
            } label: {
                HStack {
                    if model.working { ProgressView() }
                    Text(model.working ? "Syncing…" : "Sync now")
                }.frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent).controlSize(.large)
            .disabled(model.working)

            if let aside = model.setAside {
                Text("Files from before the pairing were set aside in \((aside as NSString).lastPathComponent). Nothing from there is uploaded — the server is the source of truth.")
                    .font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            Text("State: \(model.stateText) · last sync: \(lastSyncText)")
                .font(.caption).foregroundStyle(.secondary)
            if let e = model.lastError {
                Text(e).foregroundStyle(.red).font(.caption).multilineTextAlignment(.center)
            }

            Button("Open in Files") { openInFiles() }.buttonStyle(.bordered)

            Spacer()
            Button("Unpair", role: .destructive) { confirmUnpair = true }.font(.footnote)
        }
        .padding(28)
        .alert("Unpair this device?", isPresented: $confirmUnpair) {
            Button("Unpair", role: .destructive) { model.unpair() }
            Button("No", role: .cancel) {}
        } message: {
            Text("You'll need to pair again to sync.")
        }
    }

    private var lastSyncText: String {
        model.lastSyncUnix > 0
            ? Date(timeIntervalSince1970: TimeInterval(model.lastSyncUnix)).formatted(date: .abbreviated, time: .shortened)
            : "never"
    }

    // Open the Files app at our Documents/Sync folder via the shareddocuments:// scheme.
    private func openInFiles() {
        guard var comps = URLComponents(url: model.syncDirURL, resolvingAgainstBaseURL: false) else { return }
        comps.scheme = "shareddocuments"
        if let u = comps.url { UIApplication.shared.open(u) }
    }
}
