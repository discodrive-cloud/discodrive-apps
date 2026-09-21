import Foundation

/// The final operation per node across all pages returned for one Finder anchor.
public struct ChangeDelta: Sendable {
    private var latest: [String: RemoteChange] = [:]
    public var cursor: Int64

    public init(cursor: Int64) { self.cursor = cursor }

    public mutating func record(_ changes: [RemoteChange]) {
        for change in changes {
            if let previous = latest[change.nodeID], previous.seq >= change.seq { continue }
            latest[change.nodeID] = change
        }
    }

    public var updated: [String] { latest.values.filter { !$0.deleted }.map(\.nodeID).sorted() }
    public var deleted: [String] { latest.values.filter(\.deleted).map(\.nodeID).sorted() }
}
