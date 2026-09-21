import Foundation

/// System-domain operations with injectable transport; a failed list is never an empty list.
@MainActor
public enum ProviderDomainLifecycle {
    public static func close(
        matching includes: (String) -> Bool,
        list: () async throws -> [String],
        remove: (String) async throws -> Void,
        didRemove: (String) -> Void = { _ in }
    ) async -> Bool {
        do {
            for id in try await list() where includes(id) {
                do { try await remove(id); didRemove(id) }
                catch { /* The final listing determines what is still open. */ }
            }
            return try await !list().contains(where: includes)
        } catch { return false }
    }

    public static func signal(
        matching includes: (String) -> Bool,
        list: () async throws -> [String],
        notify: (String) async -> Void
    ) async throws {
        for id in try await list() where includes(id) { await notify(id) }
    }
}
