import Foundation
import Photos
import DiscoKit
import os

/// Only identifiers and modification dates enter the discovery index. Resource
/// metadata and bytes are fetched later, for the bounded upload candidates alone.
struct PhotoKitDiscoverySource: PhotoDiscoverySource {
    private static let log = Logger(subsystem: "org.discodrive.ios", category: "photo-discovery")

    func snapshot() throws -> PhotoLibraryDelta {
        try requireAccess()
        // Capture BEFORE the snapshot. Mutations during enumeration are replayed by
        // the next history pass instead of being skipped by a newer cursor.
        let token = PhotoLibrarySource.authorization == .authorized
            ? try encode(PHPhotoLibrary.shared().currentChangeToken) : nil
        let assets = PHAsset.fetchAssets(with: nil)
        var updated: [PhotoAssetStamp] = []
        updated.reserveCapacity(assets.count)
        for index in 0..<assets.count {
            try Task.checkCancellation()
            updated.append(stamp(assets.object(at: index)))
        }
        Self.log.notice("photo index snapshot: assets=\(updated.count)")
        return PhotoLibraryDelta(updated: updated, token: token)
    }

    func changes(since data: Data, consume: (PhotoLibraryDelta) throws -> Void) throws {
        try requireAccess()
        // Persistent history needs full access. Limited access reconciles only the
        // selected library, including changes to that selection, without a cursor.
        guard PhotoLibrarySource.authorization == .authorized else { throw PhotoDiscoveryError.invalidCursor }
        let token: PHPersistentChangeToken
        do {
            guard let decoded = try NSKeyedUnarchiver.unarchivedObject(ofClass: PHPersistentChangeToken.self, from: data) else {
                throw PhotoDiscoveryError.invalidCursor
            }
            token = decoded
        } catch { throw PhotoDiscoveryError.invalidCursor }
        do {
            let changes = try PHPhotoLibrary.shared().fetchPersistentChanges(since: token)
            var count = 0
            for change in changes {
                try Task.checkCancellation()
                let details = try change.changeDetails(for: .asset)
                let ids = details.insertedLocalIdentifiers.union(details.updatedLocalIdentifiers)
                let assets = PHAsset.fetchAssets(withLocalIdentifiers: Array(ids), options: nil)
                var updated: [PhotoAssetStamp] = []
                for index in 0..<assets.count {
                    try Task.checkCancellation()
                    updated.append(stamp(assets.object(at: index)))
                }
                try consume(PhotoLibraryDelta(updated: updated, deleted: Array(details.deletedLocalIdentifiers),
                                              token: try encode(change.changeToken)))
                count += updated.count
            }
            Self.log.notice("photo index delta: assets=\(count)")
        } catch let error as NSError where error.domain == PHPhotosErrorDomain &&
            (error.code == PHPhotosError.Code.persistentChangeTokenExpired.rawValue ||
             error.code == PHPhotosError.Code.persistentChangeDetailsUnavailable.rawValue) {
            throw PhotoDiscoveryError.invalidCursor
        }
    }

    private func requireAccess() throws {
        guard PhotoLibrarySource.authorization == .authorized || PhotoLibrarySource.authorization == .limited else {
            throw PhotoSourceError.notAuthorized
        }
    }
    private func stamp(_ asset: PHAsset) -> PhotoAssetStamp {
        PhotoAssetStamp(id: asset.localIdentifier, modified: asset.modificationDate ?? asset.creationDate ?? .distantPast)
    }
    private func encode(_ token: PHPersistentChangeToken) throws -> Data {
        try NSKeyedArchiver.archivedData(withRootObject: token, requiringSecureCoding: true)
    }
}
