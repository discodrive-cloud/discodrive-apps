import Foundation
import FileProvider
import DiscoKit

@MainActor
final class FilesIntegration: ObservableObject {
    static let mainID = "org.discodrive.files"
    static let vaultPrefix = "vault-"
    @Published private(set) var connected = false
    @Published private(set) var busy = false
    @Published private(set) var openVaultCount = 0
    private weak var app: AppState?

    func attach(_ app: AppState) {
        self.app = app
        app.onRemoteChange = { [weak self] in Task { await self?.signal() } }
        // Vaults stay in the app. Old provider domains are retired by connect().
    }

    func connect() async {
        guard app?.paired == true, app?.loggingOut == false, !busy else { return }
        busy = true; defer { busy = false }
        await retireVaultDomains()
        do {
            try await NSFileProviderManager.add(NSFileProviderDomain(identifier: .init(Self.mainID), displayName: "DiscoDrive"))
            connected = true
            openVaultCount = (try? await NSFileProviderManager.domains().filter { $0.identifier.rawValue.hasPrefix(Self.vaultPrefix) }.count) ?? openVaultCount
            if await app?.refresh() == true { await signal() }
        } catch { app?.lastError = app?.userMessage(for: error) ?? app?.t("status.opError") }
    }

    func disconnect() async -> Bool {
        // Wait for a registration to finish before removing the account's domains.
        while busy { await Task.detached { try? await Task.sleep(for: .milliseconds(25)) }.value }
        busy = true; defer { busy = false }
        let result = await close { $0 == Self.mainID || $0.hasPrefix(Self.vaultPrefix) }
        if result { connected = false; openVaultCount = 0 }
        return result
    }

    // Migration only: never discard a key until the system removes its old domain.
    private func retireVaultDomains() async {
        let closed = await close { $0.hasPrefix(Self.vaultPrefix) }
        openVaultCount = (try? await NSFileProviderManager.domains().filter { $0.identifier.rawValue.hasPrefix(Self.vaultPrefix) }.count) ?? openVaultCount
        if !closed { app?.lastError = app?.t("logout.vaultsStillOpen") }
    }

    func lockAll() async {
        guard !busy else { return }
        busy = true; defer { busy = false }
        await retireVaultDomains()
    }

    private func close(matching: (String) -> Bool) async -> Bool {
        await ProviderDomainLifecycle.close(matching: matching, list: {
            try await NSFileProviderManager.domains().map { $0.identifier.rawValue }
        }, remove: { id in
            try await NSFileProviderManager.remove(NSFileProviderDomain(identifier: .init(id), displayName: "DiscoDrive"))
        }, didRemove: { id in
            if id.hasPrefix(Self.vaultPrefix) { VaultKeyStore.delete(forVault: String(id.dropFirst(Self.vaultPrefix.count))) }
        })
    }

    func authenticate(_ url: URL) async {
        guard let app, let client = app.client else { return }
        do {
            await client.resetAuth()
            _ = try await client.authToken()
            guard app.client === client, app.paired else { return }
            guard await app.refresh(), app.client === client else { return }
            await signal()
        } catch { app.lastError = app.userMessage(for: error) ?? app.t("status.opError") }
    }

    func signal() async {
        guard let domains = try? await NSFileProviderManager.domains() else { return }
        openVaultCount = domains.filter { $0.identifier.rawValue.hasPrefix(Self.vaultPrefix) }.count
        for domain in domains where domain.identifier.rawValue == Self.mainID {
            guard let manager = NSFileProviderManager(for: domain) else { continue }
            for code: NSFileProviderError.Code in [.notAuthenticated, .cannotSynchronize, .serverUnreachable] {
                await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
                    manager.signalErrorResolved(NSFileProviderError(code)) { _ in done.resume() }
                }
            }
            try? await manager.signalEnumerator(for: .workingSet)
            try? await manager.signalEnumerator(for: .rootContainer)
        }
    }
}
