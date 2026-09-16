import AppKit
import FileProvider
import DiscoKit
import os

// The Finder side of the app: one domain, "DiscoDrive", registered while paired.
enum FileProviderDomain {
    // Built fresh each time: NSFileProviderDomain is a plain value the system copies.
    // The storage itself carries no name of its own: with more than one location Finder
    // labels each "<app> - <domain name>", and "DiscoDrive - DiscoDrive" said nothing.
    static var domain: NSFileProviderDomain { NSFileProviderDomain(identifier: .init("DiscoDrive"), displayName: "") }

    // No reimport, ever: on 2026-09-15 a reimportItems(below: .rootContainer) made the
    // system "create" every file it had on disk while the extension's listing was still
    // incomplete, and the extension answered by uploading empty files over 20 real ones.
    // Metadata that changes meaning goes into the item's metadataVersion instead, so the
    // affected items are re-read one by one as the system sees them.
    static func register() {
        NSFileProviderManager.add(domain) { error in
            if let error {
                Logger(subsystem: "org.discodrive.app", category: "domain").error("domain not added: \(String(describing: error), privacy: .public)")
            }
        }
    }

    static func unregister(then: (() -> Void)? = nil) {
        NSFileProviderManager.remove(domain) { error in
            if let error { NSLog("DiscoDrive: File Provider domain not removed: %@", String(describing: error)) }
            if let then { DispatchQueue.main.async(execute: then) }
        }
    }

    // The server said something changed: have the extension ask for the delta.
    static func signalChanges() {
        NSFileProviderManager(for: domain)?.signalEnumerator(for: .workingSet) { _ in }
    }
}

// An unlocked vault as a Finder location of its own: the keys go to the shared keychain,
// a domain named after the vault is registered, and Finder is taken to it. Closing drops
// both. Vaults are closed when the app quits, so the keys never outlive it.
enum VaultDomains {
    // Finder labels every location "<extension name> - <domain name>" with one icon for
    // all of them, so the open lock in the name is what tells an unlocked vault from the storage.
    static func domain(vaultID: String, name: String) -> NSFileProviderDomain {
        NSFileProviderDomain(identifier: .init(VaultCoreDomainPrefix + vaultID), displayName: "🔓 " + name)
    }

    @MainActor
    static func open(vaultID: String, name: String, keys: Data) async -> Bool {
        VaultKeyStore.save(keys, forVault: vaultID)
        let d = domain(vaultID: vaultID, name: name)
        do {
            try await NSFileProviderManager.add(d)
        } catch {
            Logger(subsystem: "org.discodrive.app", category: "vault").error("vault domain not added: \(String(describing: error), privacy: .public)")
            VaultKeyStore.delete(forVault: vaultID)
            return false
        }
        Logger(subsystem: "org.discodrive.app", category: "vault").notice("vault domain added: \(name, privacy: .public)")
        // Reveal, not open: a sandboxed app may not "open" a folder outside its container,
        // Finder shows the location by itself when asked to select it.
        if let url = try? await NSFileProviderManager(for: d)?.getUserVisibleURL(for: .rootContainer) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        }
        return true
    }

    static func close(vaultID: String, name: String) async {
        try? await NSFileProviderManager.remove(domain(vaultID: vaultID, name: name))
        VaultKeyStore.delete(forVault: vaultID)
    }

    // Every vault domain, for the tray menu and for closing them all on quit.
    static func openVaults() async -> [NSFileProviderDomain] {
        let all = (try? await NSFileProviderManager.domains()) ?? []
        return all.filter { $0.identifier.rawValue.hasPrefix(VaultCoreDomainPrefix) }
    }

    static func closeAll() async {
        for d in await openVaults() {
            let id = String(d.identifier.rawValue.dropFirst(VaultCoreDomainPrefix.count))
            try? await NSFileProviderManager.remove(d)
            VaultKeyStore.delete(forVault: id)
        }
    }
}

// Shared with the extension's VaultCore.domainPrefix; the two targets do not share code.
let VaultCoreDomainPrefix = "vault-"
