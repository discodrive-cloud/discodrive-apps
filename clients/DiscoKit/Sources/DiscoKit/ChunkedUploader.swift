import Foundation

/// Failures a caller has to tell apart.
public enum UploadError: Error {
    /// The server refused to publish because the assembled bytes did not match the size
    /// declared at init: the file changed while it was being sent (a video still recording,
    /// a download still landing). Re-stat and start over rather than retrying the session.
    case fileChangedDuringUpload
    /// The session was lost twice in a row — a real problem, not a hiccup.
    case sessionLost
}

/// Sends one file through the server's resumable chunked protocol.
///
/// Phones lose connections mid-transfer, and `PUT /sync/file` has no way back into a
/// half-finished upload — it restarts from zero. This continues from the server's
/// `next_chunk`, declares the size so a short upload is rejected rather than published,
/// and carries the content's own date so a 2019 photo is not dated today.
public struct ChunkedUploader: Sendable {

    /// Matches the desktop and Android uploaders. Each chunk is read into memory, so this
    /// is also the per-upload memory cost on the phone.
    public static let defaultChunkSize = 8 << 20

    private let api: APIClient
    private let chunkSize: Int
    /// Retries of one chunk before the upload is abandoned. A resync (409) or a lost
    /// session (404) does not count: those make forward progress.
    private let maxChunkAttempts = 3

    public init(api: APIClient, chunkSize: Int = ChunkedUploader.defaultChunkSize) {
        self.api = api
        self.chunkSize = max(1, chunkSize)
    }

    /// Uploads `fileURL` into `parentID` (nil = storage root) under `name`.
    ///
    /// `progress` is called with (sent, total) after each accepted chunk.
    public func upload(
        fileURL: URL,
        parentID: String?,
        name: String,
        modifiedAt: Date?,
        progress: (@Sendable (Int64, Int64) -> Void)? = nil
    ) async throws {
        let handle = try FileHandle(forReadingFrom: fileURL)
        defer { try? handle.close() }

        let size = Int64((try FileManager.default.attributesOfItem(atPath: fileURL.path)[.size] as? NSNumber)?.intValue ?? 0)

        var session = try await api.uploadInit(parentID: parentID, name: name, size: size, modifiedAt: modifiedAt)
        var next = session.nextChunk
        var attempts = 0
        var reInited = false

        while Int64(next) * Int64(chunkSize) < size {
            let offset = Int64(next) * Int64(chunkSize)
            try handle.seek(toOffset: UInt64(offset))
            let data = try handle.read(upToCount: chunkSize) ?? Data()
            if data.isEmpty { break }

            do {
                next = try await api.uploadChunk(uploadID: session.uploadID, index: next, data: data)
                attempts = 0
                progress?(min(offset + Int64(data.count), size), size)
            } catch APIError.http(404) {
                // The session expired (the server GCs after an hour) or the server
                // restarted. Start a fresh one — once; a second loss is not a hiccup.
                guard !reInited else { throw UploadError.sessionLost }
                reInited = true
                session = try await api.uploadInit(parentID: parentID, name: name, size: size, modifiedAt: modifiedAt)
                next = session.nextChunk
            } catch APIError.http(409) {
                // We and the server disagree on the position; the server is authoritative.
                next = try await api.uploadStatus(uploadID: session.uploadID)
            } catch {
                attempts += 1
                if attempts >= maxChunkAttempts {
                    try? await api.uploadAbort(uploadID: session.uploadID)
                    throw error
                }
                // A dropped body leaves the server's position unchanged, but ask rather
                // than assume — it also rolls back a partial chunk on its side.
                if let position = try? await api.uploadStatus(uploadID: session.uploadID) {
                    next = position
                }
            }
        }

        do {
            try await api.uploadComplete(uploadID: session.uploadID)
        } catch APIError.http(400) {
            throw UploadError.fileChangedDuringUpload
        }
    }
}
