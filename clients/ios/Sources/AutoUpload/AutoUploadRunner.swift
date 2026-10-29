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
    /// What `error` means for the person looking at the screen, when it is known.
    var problem: UploadProblem?
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

        var queued: [PhotoTransfer]
        let candidates: [PhotoAssetStamp]
        do {
            queued = try await transfers.queued()
            candidates = try journal.pendingPhotos(limit: max(0, 20 - queued.count),
                                                    excluding: Set(queued.map(\.assetID)))
        } catch { result.error = String(describing: error); return result }
        guard !candidates.isEmpty else { return result }

        let destID: String
        do { destID = try await resolveDestination() }
        catch {
            result.error = "cannot reach the server: \(error)"
            result.problem = UploadProblem.classify(error)
            return result
        }

        // The listing is what the collision check reads. Pulling it once per pass keeps the
        // check honest about files added from another device without a request per photo.
        var folder = Folder()
        do {
            for entry in try await api.listFolder(parentID: destID) { folder.add(entry) }
        } catch {
            result.error = "cannot list the destination: \(error)"
            result.problem = UploadProblem.classify(error)
            return result
        }

        for job in queued { folder.reserve(job.name) }
        let assets = PhotoLibrarySource.assets(withIDs: candidates.map(\.id))
        let byID = Dictionary(uniqueKeysWithValues: assets.map { ($0.localIdentifier, $0) })

        // Passes never overlap, so anything here is a partial export of a process that was
        // killed mid-copy (background time ran out). iOS does not clear tmp on its own schedule.
        try? FileManager.default.removeItem(at: tmp)
        var staged = queued.reduce(Int64(0)) { $0 + $1.bytes }

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
            // Each export is a full-size copy: stop while the device still has room, not
            // when a write fails. Queued uploads free their copies and start the next pass.
            switch StagingBudget.decide(stagedBytes: staged, queuedCount: queued.count, freeBytes: Self.freeBytes()) {
            case .export: break
            case .queueFull: return result
            case .lowSpace:
                if queued.isEmpty { result.blocked = .lowStorage }
                return result
            }

            var state = PlanState(item: item, created: described.created, destID: destID, folder: folder)
            defer { state.exports.removeAll() }
            do {
                try await plan(asset, &state, isCancelled: isCancelled)
                // Renames and reservations stand even when this asset does not get queued.
                folder = state.folder
                let jobs = state.jobs
                guard !isCancelled() else { break }
                if jobs.isEmpty {
                    // Every part is on the server already (or deliberately not).
                    try finish(item)
                    result.skipped += 1
                    continue
                }
                let bytes = jobs.reduce(Int64(0)) { $0 + $1.job.bytes }
                // The copies themselves may have used up the margin. With uploads already
                // waiting, drop them and let those drain first; the only item may always go.
                if !queued.isEmpty,
                   StagingBudget.decide(stagedBytes: staged + bytes, queuedCount: queued.count,
                                        freeBytes: Self.freeBytes()) == .lowSpace {
                    break
                }
                try await transfers.enqueue(jobs)
                // Staged files belong to the queue now.
                state.exports.release(jobs.map(\.file))
                // A queued copy is not "sent". The background delegate commits the
                // journal only after /complete is acknowledged (or reconciled by hash).
                for job in jobs { folder.reserve(job.job.name) }
                result.queued += 1
                queued += jobs.map(\.job)
                staged += bytes
            } catch is CancellationError {
                break
            } catch UploadError.fileChangedDuringUpload {
                // The asset changed under the upload — its modification date moved too, so
                // the next pass sees it as fresh work.
                try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                          error: "changed while uploading")
                result.deferred += 1
            } catch PlanError.noFreeName {
                try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                          error: "no free name in the destination folder")
                result.deferred += 1
            } catch where UploadProblem.classify(error) == .diskFull {
                // Every further export would fail the same way.
                try? journal.markDeferred(assetID: item.id, modified: item.modified,
                                          error: String(describing: error).prefix(300).description)
                result.deferred += 1
                if queued.isEmpty { result.blocked = .lowStorage }
                break
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

    // MARK: parts

    /// The destination folder as listed, plus the names this pass has handed out.
    struct Folder {
        private(set) var entries: [String: APIClient.FolderEntry] = [:]
        private var reserved: Set<String> = []

        mutating func add(_ entry: APIClient.FolderEntry) { entries[entry.name] = entry }
        mutating func reserve(_ name: String) { reserved.insert(name) }

        /// What sits at `name`: nothing, the same bytes, or something else.
        func state(_ name: String, sha: String) -> NameState {
            if let e = entries[name] {
                return !e.isDir && e.contentHash == sha && !sha.isEmpty ? .same : .different
            }
            return reserved.contains(name) ? .different : .absent
        }

        mutating func renamed(_ from: String, to: String) {
            guard let e = entries.removeValue(forKey: from) else { return }
            entries[to] = e
        }
    }

    /// Exported copies of one asset, removed unless the queue took them.
    struct Exports {
        private var files: [URL] = []
        mutating func add(_ url: URL) { files.append(url) }
        mutating func release(_ urls: [URL]) { files.removeAll { urls.contains($0) } }
        mutating func removeAll() {
            for url in files { try? FileManager.default.removeItem(at: url) }
            files = []
        }
    }

    enum PlanError: Error { case noFreeName }

    private var tmp: URL { FileManager.default.temporaryDirectory.appendingPathComponent("autoupload", isDirectory: true) }

    typealias Copy = (url: URL, sha: String, bytes: Int64)

    /// One asset's planning: where its jobs go and what it has exported so far.
    struct PlanState {
        let item: PhotoAssetStamp
        let created: Date
        let destID: String
        var folder: Folder
        var exports = Exports()
        var jobs: [(job: PhotoTransfer, file: URL)] = []
    }

    private func export(_ resource: PHAssetResource, _ state: inout PlanState,
                        isCancelled: @Sendable () -> Bool) async throws -> Copy {
        if isCancelled() { throw CancellationError() }
        let url = try await PhotoLibrarySource.export(resource, to: tmp)
        state.exports.add(url)
        return (url, try Self.sha256(of: url), Self.size(of: url))
    }

    private func job(_ part: PhotoPart, _ name: String, _ copy: Copy, _ state: PlanState,
                     replaces: Bool = false) -> (job: PhotoTransfer, file: URL) {
        (PhotoTransfer(account: api.transferIdentity, assetID: state.item.id, modified: state.item.modified,
                       created: state.created, parentID: state.destID, name: name, sha: copy.sha, bytes: copy.bytes,
                       part: part, replaces: replaces), copy.url)
    }

    /// A new part: a free name, or recorded without sending when it is there already.
    /// Returns the name it has on the server.
    private func place(_ part: PhotoPart, _ wanted: String, _ copy: Copy, _ state: inout PlanState) async throws -> String {
        let folder = state.folder
        switch NameResolver.resolve(wanted, exists: { folder.state($0, sha: copy.sha) }) {
        case .alreadyThere:
            try journal.recordPart(assetID: state.item.id, part: part, sha: copy.sha, serverName: wanted)
            return wanted
        case .noFreeName:
            throw PlanError.noFreeName
        case .upload(let name):
            if await inGallery(copy.sha) {
                // The gallery has it under another folder or name (imported earlier,
                // sent from another device): the same bytes would only be a copy.
                try journal.recordPart(assetID: state.item.id, part: part, sha: copy.sha, serverName: name)
            } else {
                state.jobs.append(job(part, name, copy, state))
                state.folder.reserve(name)
            }
            return name
        }
    }

    /// Decides what of the asset goes up and exports it. Parts already on the server, or in
    /// the gallery elsewhere, are recorded here; the rest are left in `state.jobs`, to be
    /// queued together.
    private func plan(_ asset: PHAsset, _ state: inout PlanState,
                      isCancelled: @Sendable () -> Bool) async throws {
        guard let parts = PhotoLibrarySource.parts(of: asset) else { throw PhotoSourceError.noResource }
        let id = state.item.id
        var recorded = try journal.parts(assetID: id)
        var originalName: String?   // where the original goes when it is not on the server yet
        var originalCopy: Copy?

        // A former version sent one file: the edit when there was one, under the original's
        // base name. Turn it into the part it is before deciding the rest.
        if let legacy = try journal.legacyUpload(assetID: id) {
            if let sha = legacy.sha, let name = legacy.serverName, recorded.isEmpty {
                let unchanged = Self.millis(legacy.modified) == Self.millis(state.item.modified)
                if !unchanged {
                    originalCopy = try await export(parts.original, &state, isCancelled: isCancelled)
                }
                let onServer = state.folder.entries[name].map { !$0.isDir && $0.contentHash == sha } ?? false
                // A rename whose answer was lost (killed, cut off) left the file under its
                // new name: carry on from there instead of taking it for deleted.
                let editName = PhotoParts.editName(original: name, resource: name)
                let renamed = !onServer
                    && (state.folder.entries[editName].map { !$0.isDir && $0.contentHash == sha } ?? false)
                switch PhotoParts.legacyKind(hasEdit: parts.edit != nil, modifiedUnchanged: unchanged,
                                             originalSHA: originalCopy?.sha, legacySHA: sha) {
                case .original:
                    try journal.recordPart(assetID: id, part: .original, sha: sha, serverName: name)
                case .edit where renamed:
                    try journal.recordPart(assetID: id, part: .edit, sha: sha, serverName: editName)
                case .edit where onServer:
                    // Rename first, then the original under the freed name: otherwise the
                    // original would find its name taken and go up as "-1". An edit reverted
                    // since goes the same way: renamed, then trashed below.
                    var kept = name
                    if state.folder.state(editName, sha: sha) == .absent, let entry = state.folder.entries[name] {
                        try await api.rename(nodeID: entry.id, newName: editName)
                        state.folder.renamed(name, to: editName)
                        kept = editName
                    }
                    try journal.recordPart(assetID: id, part: .edit, sha: sha, serverName: kept)
                case .edit:
                    // Deleted or replaced on the server: what the user did there stands.
                    // Nothing of this asset goes up again, unless it is edited anew.
                    try journal.recordPart(assetID: id, part: .edit, sha: sha, serverName: name)
                    try journal.recordPart(assetID: id, part: .original, sha: "", serverName: "")
                    if parts.live != nil {
                        try journal.recordPart(assetID: id, part: .live, sha: "", serverName: "")
                    }
                }
                recorded = try journal.parts(assetID: id)
            }
            try journal.forgetLegacy(assetID: id)
        }

        // The original and the Live video go up once. An edit already there (renamed from a
        // former upload) names the original: it has to land beside it to be paired.
        if recorded[.original] == nil, let edit = recorded[.edit] {
            originalName = PhotoParts.originalName(forEdit: edit.serverName, resource: parts.original.originalFilename)
        }
        var base = recorded[.original]?.serverName ?? ""
        if recorded[.original] == nil {
            var copy = originalCopy
            if copy == nil { copy = try await export(parts.original, &state, isCancelled: isCancelled) }
            base = try await place(.original, originalName ?? parts.original.originalFilename, copy!, &state)
        }
        if base.isEmpty { base = parts.original.originalFilename }
        if let live = parts.live, recorded[.live] == nil {
            let copy = try await export(live, &state, isCancelled: isCancelled)
            _ = try await place(.live, PhotoParts.liveName(original: base, resource: live.originalFilename), copy, &state)
        }

        // The edit follows every change and goes when the edit is reverted.
        if let edit = parts.edit {
            let copy = try await export(edit, &state, isCancelled: isCancelled)
            if let rec = recorded[.edit], rec.sha == copy.sha {
                // Unchanged: the asset moved for another reason (favourite, album).
            } else if let rec = recorded[.edit], !rec.serverName.isEmpty,
                      state.folder.entries[rec.serverName].map({ !$0.isDir && ($0.contentHash == rec.sha || $0.contentHash == copy.sha) }) ?? true {
                // A new version of the edit this phone sent (or of nothing, when the user
                // deleted it). A file put there since is the user's: it gets a name of its own.
                if state.folder.state(rec.serverName, sha: copy.sha) == .same {
                    try journal.recordPart(assetID: id, part: .edit, sha: copy.sha, serverName: rec.serverName)
                } else {
                    state.jobs.append(job(.edit, rec.serverName, copy, state, replaces: true))
                }
            } else {
                _ = try await place(.edit, PhotoParts.editName(original: base, resource: edit.originalFilename), copy, &state)
            }
        } else if let rec = recorded[.edit] {
            // Reverted in Photos. Only the file this phone sent goes to the trash; one changed
            // on the server since is the user's.
            if let entry = state.folder.entries[rec.serverName], !entry.isDir, entry.contentHash == rec.sha {
                try await api.delete(nodeID: entry.id)
            }
            try journal.forgetPart(assetID: id, part: .edit)
        }
    }

    private static func millis(_ d: Date) -> Int64 { Int64((d.timeIntervalSince1970 * 1000).rounded()) }

    private static func size(of url: URL) -> Int64 {
        Int64((try? FileManager.default.attributesOfItem(atPath: url.path)[.size] as? NSNumber)??.int64Value ?? 0)
    }

    /// Every part of this version is on the server: the asset is done.
    private func finish(_ item: PhotoAssetStamp) throws {
        let original = try journal.parts(assetID: item.id)[.original]
        try journal.markSent(assetID: item.id, modified: item.modified, bytes: 0,
                             sha: original?.sha, serverName: original?.serverName ?? "")
    }

    /// Whether the gallery already holds this content. A failed check uploads: a copy costs
    /// space, a wrong skip would lose the photo.
    private func inGallery(_ sha: String) async -> Bool {
        (try? await api.knownPhotoHashes([sha]).contains(sha)) ?? false
    }

    /// `/DeviceUploads/<device>`, created on first use and cached by node id afterwards.
    private func resolveDestination() async throws -> String {
        if let cached = settings.destID { return cached }
        let root = try await api.ensureFolder(parentID: nil, name: Self.destRoot)
        let dest = try await api.ensureFolder(parentID: root, name: Self.deviceFolder)
        settings.destID = dest
        return dest
    }

    /// Space the system can give an app for work the user asked for, purgeable caches
    /// included. Nil when the volume does not say.
    static func freeBytes() -> Int64? {
        let home = URL(fileURLWithPath: NSHomeDirectory())
        return (try? home.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey]))?
            .volumeAvailableCapacityForImportantUsage
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
