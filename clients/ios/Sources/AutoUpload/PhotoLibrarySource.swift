import Foundation
import Photos

/// One photo or video worth uploading, described without touching its bytes.
struct PhotoItem: Identifiable, Sendable {
    let id: String            // PHAsset.localIdentifier
    let filename: String      // original name from the asset resource, e.g. IMG_0042.HEIC
    let created: Date         // when the picture was taken — what the server should date it
    let modified: Date        // bumps when the photo is edited; part of the journal identity
    let isVideo: Bool
}

enum PhotoSourceError: Error {
    case notAuthorized
    case exportFailed(String)
    case noResource
}

/// The iOS side of "which folder do we upload": there are no folders, there is the photo
/// library, and it is only reachable through PhotoKit.
///
/// Nothing here writes to the library. Assets are read, exported to a temporary file for
/// the upload, and the temp copy is deleted afterwards.
enum PhotoLibrarySource {

    /// Read access, asking once. Returns false when the user said no or granted nothing.
    @discardableResult
    static func requestAccess() async -> PHAuthorizationStatus {
        await withCheckedContinuation { cont in
            PHPhotoLibrary.requestAuthorization(for: .readWrite) { cont.resume(returning: $0) }
        }
    }

    static var authorization: PHAuthorizationStatus {
        PHPhotoLibrary.authorizationStatus(for: .readWrite)
    }

    /// Everything in the library, newest first — a photo just taken should not queue behind
    /// an archive of thousands.
    ///
    /// `mediaOnly` has no meaning here (a photo library holds photos), so unlike Android
    /// there is no filter: the choice a user makes on iOS is the album, not the file type.
    static func scan(limit: Int? = nil) -> [PhotoItem] {
        let options = PHFetchOptions()
        options.sortDescriptors = [NSSortDescriptor(key: "creationDate", ascending: false)]
        if let limit { options.fetchLimit = limit }
        let assets = PHAsset.fetchAssets(with: options)

        var out: [PhotoItem] = []
        out.reserveCapacity(assets.count)
        assets.enumerateObjects { asset, _, _ in
            guard let item = describe(asset) else { return }
            out.append(item)
        }
        return out
    }

    /// Fetches assets by identifier, for turning journal rows back into work.
    static func assets(withIDs ids: [String]) -> [PHAsset] {
        guard !ids.isEmpty else { return [] }
        let result = PHAsset.fetchAssets(withLocalIdentifiers: ids, options: nil)
        var out: [PHAsset] = []
        result.enumerateObjects { asset, _, _ in out.append(asset) }
        return out
    }

    static func describe(_ asset: PHAsset) -> PhotoItem? {
        let resources = PHAssetResource.assetResources(for: asset)
        // An edited photo carries both the original and the adjusted resource; the one the
        // user sees is what should go up.
        let preferred = resources.first { $0.type == .fullSizePhoto || $0.type == .fullSizeVideo }
            ?? resources.first { $0.type == .photo || $0.type == .video }
            ?? resources.first
        guard let resource = preferred else { return nil }
        return PhotoItem(
            id: asset.localIdentifier,
            filename: resource.originalFilename,
            created: asset.creationDate ?? asset.modificationDate ?? Date(),
            modified: asset.modificationDate ?? asset.creationDate ?? Date(),
            isVideo: asset.mediaType == .video,
        )
    }

    /// Writes the asset's bytes to a temporary file and hands back the URL.
    ///
    /// `isNetworkAccessAllowed` matters: with "Optimise iPhone Storage" the full-size
    /// original lives in iCloud and a local-only read would either fail or silently hand
    /// back a thumbnail-grade copy.
    static func export(_ asset: PHAsset, to directory: URL) async throws -> (url: URL, item: PhotoItem) {
        guard let item = describe(asset) else { throw PhotoSourceError.noResource }
        let resources = PHAssetResource.assetResources(for: asset)
        let preferred = resources.first { $0.type == .fullSizePhoto || $0.type == .fullSizeVideo }
            ?? resources.first { $0.type == .photo || $0.type == .video }
            ?? resources.first
        guard let resource = preferred else { throw PhotoSourceError.noResource }

        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let dest = directory.appendingPathComponent(UUID().uuidString + "-" + item.filename)
        try? FileManager.default.removeItem(at: dest)

        let options = PHAssetResourceRequestOptions()
        options.isNetworkAccessAllowed = true

        try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, Error>) in
            PHAssetResourceManager.default().writeData(for: resource, toFile: dest, options: options) { error in
                if let error { cont.resume(throwing: PhotoSourceError.exportFailed(error.localizedDescription)) }
                else { cont.resume() }
            }
        }
        return (dest, item)
    }
}
