import Foundation
import Photos
import DiscoKit

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
        let original = resources.first { $0.type == .photo || $0.type == .video }
        return PhotoItem(
            id: asset.localIdentifier,
            filename: NameResolver.uploadName(resource: resource.originalFilename,
                                              original: original?.originalFilename),
            created: asset.creationDate ?? asset.modificationDate ?? Date(),
            modified: asset.modificationDate ?? asset.creationDate ?? .distantPast,
            isVideo: asset.mediaType == .video,
        )
    }

    /// The resources of an asset by part: the camera's file, the adjusted render when the
    /// asset is edited, a Live Photo's video. The edit of a Live Photo's video is left out:
    /// the gallery would hang both videos on the card.
    struct Parts {
        let original: PHAssetResource
        let edit: PHAssetResource?
        let live: PHAssetResource?
    }

    static func parts(of asset: PHAsset) -> Parts? {
        let resources = PHAssetResource.assetResources(for: asset)
        let video = asset.mediaType == .video
        guard let original = resources.first(where: { $0.type == (video ? .video : .photo) })
            ?? resources.first(where: { $0.type == .photo || $0.type == .video }) else { return nil }
        return Parts(original: original,
                     edit: resources.first { $0.type == (video ? .fullSizeVideo : .fullSizePhoto) },
                     live: video ? nil : resources.first { $0.type == .pairedVideo })
    }

    /// Writes one resource's bytes to a temporary file and hands back the URL.
    ///
    /// `isNetworkAccessAllowed` matters: with "Optimise iPhone Storage" the full-size
    /// original lives in iCloud and a local-only read would either fail or silently hand
    /// back a thumbnail-grade copy.
    nonisolated(nonsending) static func export(_ resource: PHAssetResource, to directory: URL) async throws -> URL {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let dest = directory.appendingPathComponent(UUID().uuidString + "-" + resource.originalFilename)
        try? FileManager.default.removeItem(at: dest)

        let options = PHAssetResourceRequestOptions()
        options.isNetworkAccessAllowed = true

        let export = try PhotoExportRequest(destination: dest)
        do {
            try await withTaskCancellationHandler {
                try await withCheckedThrowingContinuation { (cont: CheckedContinuation<Void, Error>) in
                    export.start(resource: resource, options: options, continuation: cont)
                }
            } onCancel: { export.cancel() }
        } catch {
            try? FileManager.default.removeItem(at: dest)
            throw error
        }
        return dest
    }
}


/// PhotoKit's writeData API cannot be cancelled. Stream its resource request into a
/// file instead so an expired background budget releases discovery immediately.
private final class PhotoExportRequest: @unchecked Sendable {
    private let lock = NSLock()
    private let manager = PHAssetResourceManager.default()
    private let handle: FileHandle
    private var requestID: PHAssetResourceDataRequestID?
    private var continuation: CheckedContinuation<Void, Error>?
    private var outcome: Result<Void, Error>?

    init(destination: URL) throws {
        guard FileManager.default.createFile(atPath: destination.path, contents: nil,
                                             attributes: [.protectionKey: FileProtectionType.completeUntilFirstUserAuthentication]) else {
            throw PhotoSourceError.exportFailed("cannot create export")
        }
        handle = try FileHandle(forWritingTo: destination)
    }

    func start(resource: PHAssetResource, options: PHAssetResourceRequestOptions,
               continuation: CheckedContinuation<Void, Error>) {
        lock.lock()
        if let outcome { lock.unlock(); continuation.resume(with: outcome); return }
        self.continuation = continuation
        lock.unlock()
        let id = manager.requestData(for: resource, options: options, dataReceivedHandler: { [self] data in
            lock.lock()
            guard outcome == nil else { lock.unlock(); return }
            do { try handle.write(contentsOf: data); lock.unlock() }
            catch { lock.unlock(); finish(.failure(error)) }
        }, completionHandler: { [self] error in
            finish(error.map { .failure($0) } ?? .success(()))
        })
        lock.lock()
        requestID = id
        let finished = outcome != nil
        lock.unlock()
        if finished { manager.cancelDataRequest(id) }
    }

    func cancel() { finish(.failure(CancellationError())) }

    private func finish(_ result: Result<Void, Error>) {
        lock.lock()
        guard outcome == nil else { lock.unlock(); return }
        var result = result
        do { try handle.close() } catch { result = .failure(error) }
        outcome = result
        let continuation = self.continuation
        self.continuation = nil
        let id = requestID
        lock.unlock()
        if case .failure = result, let id { manager.cancelDataRequest(id) }
        continuation?.resume(with: result)
    }
}
