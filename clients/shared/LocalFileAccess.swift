import Foundation
import DiscoKit

/// Platform-owned files (File Provider on macOS) can be the browser's local copy.
@MainActor
protocol LocalFileAccess: AnyObject, Sendable {
    func status(of node: Node) async throws -> LocalStatus
    func download(_ node: Node) async throws -> URL
    func evict(_ node: Node) async throws
    func cachedIDs() async throws -> [String]
    func rootURL() async throws -> URL
    func close()
}

extension LocalFileAccess {
    func cachedIDs() async throws -> [String] { [] }
    func rootURL() async throws -> URL { throw CocoaError(.fileNoSuchFile) }
}
