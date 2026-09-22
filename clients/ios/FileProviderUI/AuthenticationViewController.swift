import UIKit
import SwiftUI
import FileProvider
import FileProviderUI
import DiscoKit

final class AuthenticationViewController: FPUIActionExtensionViewController {
    private func present(_ action: String?, items: [NSFileProviderItemIdentifier] = []) {
        loadViewIfNeeded()
        let content = ProviderActionView(action: action, items: items, domain: extensionContext.domainIdentifier,
            finish: { [weak self] in self?.extensionContext.completeRequest() },
            cancel: { [weak self] in self?.extensionContext.cancelRequest(withError: NSError(domain: FPUIErrorDomain, code: 0)) })
        let host = UIHostingController(rootView: content)
        addChild(host); host.view.translatesAutoresizingMaskIntoConstraints = false; view.addSubview(host.view)
        NSLayoutConstraint.activate([
            host.view.leadingAnchor.constraint(equalTo: view.leadingAnchor), host.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            host.view.topAnchor.constraint(equalTo: view.topAnchor), host.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
        ])
        host.didMove(toParent: self)
    }
    override func prepare(forError error: Error) { present(nil) }
    override func prepare(forAction actionIdentifier: String, itemIdentifiers: [NSFileProviderItemIdentifier]) { present(actionIdentifier, items: itemIdentifiers) }
}

private struct ProviderActionView: View {
    let action: String?
    let items: [NSFileProviderItemIdentifier]
    let domain: NSFileProviderDomainIdentifier?
    let finish: () -> Void
    let cancel: () -> Void
    @State private var busy = false
    @State private var error: String?
    @State private var success = false
    private func t(_ key: String) -> String { L10n.t(key, Locale.preferredLanguages.first?.components(separatedBy: "-").first ?? "en") }
    var body: some View {
        NavigationStack {
            Form {
                if action == nil { Text(t("ios.files.authenticate")) }
                else if success { Text(t("ios.files.evicted")) }
                else {
                    Text(t("ios.files.evictHint"))
                    if let error { Text(error).foregroundStyle(.red) }
                    if busy { ProgressView() }
                    Button(t("menu.removeLocal")) { Task { await perform() } }
                        .disabled(busy)
                }
            }
            .navigationTitle(t(action == nil ? "ios.files.title" : "menu.removeLocal"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button(t(success ? "dialog.done" : "dialog.cancel")) { if success { finish() } else { cancel() } }.disabled(busy) } }
            .interactiveDismissDisabled(busy)
        }
    }
    private func perform() async {
        guard !busy else { return }; busy = true; error = nil; defer { busy = false }
        do {
            guard action == "org.discodrive.evict", let domain, !items.isEmpty,
                  let manager = NSFileProviderManager(for: NSFileProviderDomain(identifier: domain, displayName: "DiscoDrive")) else { throw NSFileProviderError(.noSuchItem) }
            for item in items { try await manager.evictItem(identifier: item) }
            success = true
        } catch {
            let details = [error.localizedDescription, (error as NSError).localizedFailureReason]
                .compactMap { $0 }.joined(separator: "\n")
            self.error = t("status.opError") + "\n" + details
        }
    }
}
