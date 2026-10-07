import Foundation

public struct PhotoAssetStamp: Sendable, Equatable {
    public let id: String
    public let modified: Date
    public init(id: String, modified: Date) { self.id = id; self.modified = modified }
}
public struct PhotoLibraryDelta: Sendable {
    public let updated: [PhotoAssetStamp]
    public let deleted: [String]
    public let token: Data?
    public init(updated: [PhotoAssetStamp], deleted: [String] = [], token: Data?) {
        self.updated = updated; self.deleted = deleted; self.token = token
    }
}
public enum PhotoDiscoveryError: Error { case invalidCursor }
public protocol PhotoDiscoverySource {
    func snapshot() throws -> PhotoLibraryDelta
    func changes(since: Data, consume: (PhotoLibraryDelta) throws -> Void) throws
}
public enum PhotoDiscovery {
    /// Each page and its cursor commit together, independently of the upload queue.
    public static func reconcile(source: any PhotoDiscoverySource, journal: UploadJournal,
                                 seedPreexisting: Bool) throws {
        try Task.checkCancellation()
        if let cursor = try journal.photoDiscoveryCursor() {
            do {
                try source.changes(since: cursor) { try journal.applyPhotoDiscovery($0) }
                return
            } catch PhotoDiscoveryError.invalidCursor {
                // Recovery must never classify missed photos as pre-existing.
                try journal.applyPhotoDiscovery(source.snapshot(), replacing: true)
                return
            }
        }
        try journal.applyPhotoDiscovery(source.snapshot(), replacing: true, seedPreexisting: seedPreexisting)
    }
}
