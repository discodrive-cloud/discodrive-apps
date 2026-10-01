import Foundation

// Keep failed removals until the server confirms them, including across restarts.
public struct VaultConflictCleanup: Sendable {
    public let index: IndexStore

    public init(index: IndexStore) {
        self.index = index
    }

    public func retry(delete: (String) async throws -> Void) async throws {
        for nodeID in try index.pendingVaultConflictRemovals() {
            do { try await delete(nodeID) }
            catch APIError.http(404), APIError.nodeNotFound { /* already removed */ }
            catch { continue }
            try index.finishVaultConflictRemoval(nodeID: nodeID)
        }
    }
}
