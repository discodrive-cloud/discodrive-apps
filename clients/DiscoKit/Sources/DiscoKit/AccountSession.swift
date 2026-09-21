import Foundation

/// Owns work that must finish before an account's files and databases are replaced.
@MainActor
public final class AccountSession {
    public private(set) var isActive = true
    private struct Work {
        let cancel: () -> Void
        let wait: () async -> Void
    }
    private var work: [UUID: Work] = [:]

    public init() {}

    public func check() throws {
        guard isActive else { throw CancellationError() }
        try Task.checkCancellation()
    }

    public func perform<T: Sendable>(_ operation: @escaping @Sendable () async throws -> T) async throws -> T {
        try check()
        let id = UUID()
        let task = Task { try await operation() }
        work[id] = Work(cancel: { task.cancel() }, wait: { _ = await task.result })
        defer { work.removeValue(forKey: id) }
        let result = try await withTaskCancellationHandler {
            try await task.value
        } onCancel: { task.cancel() }
        // Some transports finish successfully even after cancellation. Do not hand those
        // bytes to a caller that is about to write into the next account's directory.
        try check()
        return result
    }

    public func invalidate() {
        isActive = false
        for task in work.values { task.cancel() }
    }

    public func stop() async {
        let pending = Array(work.values)
        invalidate()
        for task in pending { await task.wait() }
    }
}
