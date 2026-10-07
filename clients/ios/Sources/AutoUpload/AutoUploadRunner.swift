import Foundation
import CryptoKit
import UIKit
import DiscoKit
import Photos

/// Outcome of one pass.
struct RunResult: Sendable {
    var uploaded = 0
    var queued = 0
    var skipped = 0
    var deferred = 0
    var blocked: UploadBlock = .none
    var error: String?
}

/// One pass of auto-upload: find the photos that have not been dealt with, decide a name for
/// each, send it, record it.
///
/// The photo library is only ever read. Nothing is deleted from it, and the temporary export
/// of each asset is removed as soon as its upload finishes.
actor AutoUploadRunner {

    /// Destination on the server. The device name keeps two phones from mixing.
    static let destRoot = "DeviceUploads"
    static var deviceFolder: String {
        let name = UIDevice.current.name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "iPhone" : name
    }

    /// Export failures retry on later discovery passes. Persistent transfer failures
    /// have their own exponential backoff; neither path parks an asset for a whole day.
    static let maxAttempts = 1
    static let retryAfter: TimeInterval = 60

    private let api: APIClient
    private let journal: UploadJournal
    private let settings: AutoUploadSettings
    private let transfers: BackgroundPhotoUploader

    init(api: APIClient, journal: UploadJournal, settings: AutoUploadSettings, transfers: BackgroundPhotoUploader) {
        self.api = api
        self.journal = journal
        self.settings = settings
        self.transfers = transfers
    }

    /// Persist discovery before doing any network work. Existing installations take
    /// one metadata-only snapshot; subsequent passes consume PhotoKit change history.
    func discover() throws {
        try PhotoDiscovery.reconcile(source: PhotoKitDiscoverySource(), journal: journal,
                                     seedPreexisting: !settings.seeded)
        settings.seeded = true
    }

    /// Uploads everything new. `progress` is called before each asset with (done, total, name).
    func runOnce(progress: @Sendable (Int, Int, String) -> Void = { _, _, _ in },
                 isCancelled: @Sendable () -> Bool = { false }) async -> RunResult {
        var result = RunResult()

        guard PhotoLibrarySource.authorization == .authorized || PhotoLibrarySource.authorization == .limited else {
            result.error = "no access to the photo library"
            return result
        }

        let queued: [PhotoTransfer]
        let candidates: [PhotoAssetStamp]
        do {
            queued = try await transfers.queued()
            candidates = try journal.pendingPhotos(limit: max(0, 20 - queued.count),
                                                    excluding: Set(queued.map(\.assetID)))
        } catch { result.error = String(describing: error); return result }
        guard !candidates.isEmpty else { return result }

        let destID: String
        do { destID = try await resolveDestination() }
        catch { result.error = "cannot reach the server: \(error)"; return result }

        // The listing is what the collision check reads. Pulling it once per pass keeps the
        // check honest about files added from another device without a request per photo.
        var taken: [String: String] = [:]   // name → content hash ("" when unknown)
        do {
            for entry in try await api.listFolder(parentID: destID) {
                taken[entry.name] = entry.isDir ? "" : (entry.contentHash ?? "")
            }
        } catch {
            result.error = "cannot list the destination: \(error)"
            return result
        }

        for job in queued { taken[job.name] = "" }
        let assets = PhotoLibrarySource.assets(withIDs: candidates.map(\.id))
        let byID = Dictionary(uniqueKeysWithValues: assets.map { ($0.localIdentifier, $0) })

        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("autoupload", isDirectory: true)

        for (i, item) in candidates.enumerated() {
            if isCancelled() { break }
            guard let asset = byID[item.id] else {
                // Removed from the library or from the authorized selection.
                try? journal.forgetPendingPhoto(item)
                continue
            }
            guard let described = PhotoLibrarySource.describe(asset) else {
                try? journal.markDeferred(assetID: item.id, modified: item.modified, error: "no photo resource")
                result.deferred += 1
                continue
            }
            // Metadata may change after discovery; persist the version actually exported.
            let item = PhotoAssetStamp(id: described.id, modified: described.modified)
            do { try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [item], token: nil)) }
            catch { result.error = String(describing: error); return result }
            if (try? journal.isKnown(assetID: item.id, modified: item.modified)) == true { continue }
            progress(i, candidates.count, described.filename)
            if (try? journal.mayRetry(assetID: item.id, maxAttempts: Self.maxAttempts,
                                      retryAfter: Self.retryAfter)) == false {
                result.deferred += 1
                continue
            }

            do {
                let (url, described) = try await PhotoLibrarySource.export(asset, to: tmp)
                defer { try? FileManager.default.removeItem(at: url) }

                let sha = try Self.sha256(of: url)
                let name: String
                switch NameResolver.resolve(described.filename, exists: { candidate in
                    guard let hash = taken[candidate] else { return .absent }
                    return hash == sha && !hash.isEmpty ? .same : .different
                }) {
                case .upload(let free):
                    name = free
                case .alreadyThere:
                    // Already on the server byte for byte: record it so the next pass does
                    // not export and hash it again.
                    try journal.markSent(assetID: item.id, modified: item.modified,
                                         bytes: 0, sha: sha, serverName: described.filename)
                    result.skipped += 1
                    continue
                case .noFreeName:
                    try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                              error: "no free name in the destination folder")
                    result.deferred += 1
                    continue
                }

                let bytes = Int64((try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? NSNumber)??.int64Value ?? 0)
                guard !isCancelled() else { break }
                let job = PhotoTransfer(account: api.transferIdentity, assetID: item.id,
                                        modified: item.modified, created: described.created,
                                        parentID: destID, name: name, sha: sha, bytes: bytes)
                try await transfers.enqueue(job, exportedFile: url)
                // A queued copy is not "sent". The background delegate commits the
                // journal only after /complete is acknowledged (or reconciled by hash).
                taken[name] = ""
                result.queued += 1
            } catch is CancellationError {
                break
            } catch UploadError.fileChangedDuringUpload {
                // The asset changed under the upload — its modification date moved too, so
                // the next pass sees it as fresh work.
                try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                          error: "changed while uploading")
                result.deferred += 1
            } catch {
                try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                          error: String(describing: error).prefix(300).description)
                result.deferred += 1
                // The destination may be gone on the server: resolve it again next pass
                // (ensuring a folder that exists is a no-op).
                settings.destID = nil
            }
        }
        return result
    }

    /// `/DeviceUploads/<device>`, created on first use and cached by node id afterwards.
    private func resolveDestination() async throws -> String {
        if let cached = settings.destID { return cached }
        let root = try await api.ensureFolder(parentID: nil, name: Self.destRoot)
        let dest = try await api.ensureFolder(parentID: root, name: Self.deviceFolder)
        settings.destID = dest
        return dest
    }

    static func sha256(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
