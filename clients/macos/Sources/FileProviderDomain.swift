import AppKit
import FileProvider
import DiscoKit
import os

// The Finder side of the app: one domain, "DiscoDrive", registered while paired.
@MainActor
enum FileProviderDomain {
    private static var registration: Task<Void, Never>?
    private static var closing = false
    private static var closeTask: Task<Bool, Never>?
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
        guard !closing, registration == nil else { return }
        registration = Task {
            defer { registration = nil }
            do { try await NSFileProviderManager.add(domain) }
            catch { NSLog("DiscoDrive: File Provider domain not added: %@", String(describing: error)) }
        }
    }

    // Used by DEBUG re-pair as well as normal logout. A failed removal never starts
    // registration of the next account's domain.
    static func unregister(then: (() -> Void)? = nil) {
        Task { if await close() { then?() } }
    }

    static func close() async -> Bool {
        if let closeTask { return await closeTask.value }
        closing = true
        let operation = Task {
            // An add already submitted to the system must settle before we list/remove.
            await registration?.value
            return await ProviderDomainLifecycle.close(
                matching: { $0 == domain.identifier.rawValue },
                list: { try await NSFileProviderManager.domains().map { $0.identifier.rawValue } },
                remove: { _ in try await NSFileProviderManager.remove(domain) }
            )
        }
        closeTask = operation
        let closed = await operation.value
        closeTask = nil; closing = false
        return closed
    }

    static func closeForLogout() async -> Bool {
        guard await VaultDomains.closeAll() else { return false }
        return await close()
    }

    // Every unlocked vault has its own enumerator, independent of the storage domain.
    static func signalChanges() {
        Task {
            do {
                try await ProviderDomainLifecycle.signal(
                    matching: { $0 == domain.identifier.rawValue || $0.hasPrefix(VaultCoreDomainPrefix) },
                    list: { try await NSFileProviderManager.domains().map { $0.identifier.rawValue } },
                    notify: { id in
                        let d = NSFileProviderDomain(identifier: .init(id), displayName: "")
                        guard let manager = NSFileProviderManager(for: d) else { return }
                        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
                            manager.signalEnumerator(for: .workingSet) { _ in done.resume() }
                        }
                    }
                )
            } catch { NSLog("DiscoDrive: domains could not be signalled: %@", String(describing: error)) }
        }
    }

}

// An unlocked vault as a Finder location of its own: the keys go to the shared keychain,
// a domain named after the vault is registered, and Finder is taken to it. Closing drops
// both. Vaults are closed when the app quits, so the keys never outlive it.
@MainActor
enum VaultDomains {
    private static var pendingOpens: [UUID: Task<Void, Error>] = [:]
    private static var closing = false
    private static var closeTask: Task<Bool, Never>?
    // Finder labels every location "<extension name> - <domain name>" with one icon for
    // all of them, so the open lock in the name is what tells an unlocked vault from the storage.
    static func domain(vaultID: String, name: String) -> NSFileProviderDomain {
        NSFileProviderDomain(identifier: .init(VaultCoreDomainPrefix + vaultID), displayName: "🔓 " + name)
    }

    @MainActor
    static func open(vaultID: String, name: String, keys: Data) async -> Bool {
        guard !closing else { return false }
        VaultKeyStore.save(keys, forVault: vaultID)
        let d = domain(vaultID: vaultID, name: name)
        let operationID = UUID()
        let operation = Task { try await NSFileProviderManager.add(d) }
        pendingOpens[operationID] = operation
        defer { pendingOpens.removeValue(forKey: operationID) }
        do {
            try await operation.value
        } catch {
            Logger(subsystem: "org.discodrive.app", category: "vault").error("vault domain not added: \(String(describing: error), privacy: .public)")
            VaultKeyStore.delete(forVault: vaultID)
            return false
        }
        guard !closing else { return false }
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

    // True when no vault is open afterwards — asked of the system again, not assumed: a
    // domain that could not be removed still has an extension holding its keys.
    @discardableResult
    static func closeAll() async -> Bool {
        if let closeTask { return await closeTask.value }
        closing = true
        let operation = Task {
            let pending = Array(pendingOpens.values)
            for opening in pending { _ = await opening.result }
            return await ProviderDomainLifecycle.close(
                matching: { $0.hasPrefix(VaultCoreDomainPrefix) },
                list: { try await NSFileProviderManager.domains().map { $0.identifier.rawValue } },
                remove: { id in
                    try await NSFileProviderManager.remove(NSFileProviderDomain(identifier: .init(id), displayName: ""))
                },
                didRemove: { id in VaultKeyStore.delete(forVault: String(id.dropFirst(VaultCoreDomainPrefix.count))) }
            )
        }
        closeTask = operation
        let closed = await operation.value
        closeTask = nil; closing = false
        return closed
    }

}

// Shared with the extension's VaultCore.domainPrefix; the two targets do not share code.
let VaultCoreDomainPrefix = "vault-"
