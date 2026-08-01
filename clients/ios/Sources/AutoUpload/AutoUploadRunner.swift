import Foundation
import CryptoKit
import UIKit
import DiscoKit
import Photos

/// Outcome of one pass.
struct RunResult: Sendable {
    var uploaded = 0
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
    static let destRoot = "Camera Uploads"
    static var deviceFolder: String {
        let name = UIDevice.current.name.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? "iPhone" : name
    }

    /// Give up on an asset after this many failed passes; it stays visible in the log.
    static let maxAttempts = 5

    private let api: APIClient
    private let journal: UploadJournal
    private let settings: AutoUploadSettings

    init(api: APIClient, journal: UploadJournal, settings: AutoUploadSettings) {
        self.api = api
        self.journal = journal
        self.settings = settings
    }

    /// Records everything currently in the library as pre-existing, so switching the feature
    /// on does not push years of pictures over a cellular link. Runs once.
    func seedIfNeeded() throws -> Int {
        guard !settings.seeded else { return 0 }
        let existing = PhotoLibrarySource.scan().map { (id: $0.id, modified: $0.modified) }
        try journal.seedPreexisting(existing)
        settings.seeded = true
        return existing.count
    }

    /// Uploads everything new. `progress` is called before each asset with (done, total, name).
    func runOnce(progress: @Sendable (Int, Int, String) -> Void = { _, _, _ in },
                 isCancelled: @Sendable () -> Bool = { false }) async -> RunResult {
        var result = RunResult()

        guard PhotoLibrarySource.authorization == .authorized || PhotoLibrarySource.authorization == .limited else {
            result.error = "no access to the photo library"
            return result
        }

        // Resolve the destination first: a pass that cannot address the server has nothing
        // to do, and creating the folder per photo would be silly.
        let destID: String
        do {
            destID = try await resolveDestination()
        } catch {
            result.error = "cannot reach the server: \(error)"
            return result
        }

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

        let candidates = PhotoLibrarySource.scan().filter { item in
            (try? journal.isKnown(assetID: item.id, modified: item.modified)) != true
        }
        let assets = PhotoLibrarySource.assets(withIDs: candidates.map(\.id))
        let byID = Dictionary(uniqueKeysWithValues: assets.map { ($0.localIdentifier, $0) })

        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("autoupload", isDirectory: true)
        let uploader = ChunkedUploader(api: api)

        for (i, item) in candidates.enumerated() {
            if isCancelled() { break }
            progress(i, candidates.count, item.filename)
            guard let asset = byID[item.id] else { continue }
            if (try? journal.attempts(assetID: item.id)) ?? 0 >= Self.maxAttempts {
                result.deferred += 1
                continue
            }

            do {
                let (url, described) = try await PhotoLibrarySource.export(asset, to: tmp)
                defer { try? FileManager.default.removeItem(at: url) }

                let sha = try Self.sha256(of: url)
                let name = NameResolver.resolve(described.filename) { candidate in
                    guard let hash = taken[candidate] else { return .absent }
                    return hash == sha && !hash.isEmpty ? .same : .different
                }
                guard let name else {
                    // Already on the server byte for byte (or no free name): record it so
                    // the next pass does not export and hash it again.
                    try journal.markSent(assetID: item.id, modified: item.modified,
                                         bytes: 0, sha: sha, serverName: described.filename)
                    result.skipped += 1
                    continue
                }

                let bytes = Int64((try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? NSNumber)??.int64Value ?? 0)
                try await uploader.upload(fileURL: url, parentID: destID, name: name,
                                          modifiedAt: described.created)
                // Claim the name for the rest of this pass: the listing was read once, so
                // two photos could otherwise be handed the same free name.
                taken[name] = sha
                try journal.markSent(assetID: item.id, modified: item.modified,
                                     bytes: bytes, sha: sha, serverName: name)
                result.uploaded += 1
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
            }
        }
        return result
    }

    /// `/Camera Uploads/<device>`, created on first use and cached by node id afterwards.
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
