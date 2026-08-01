import Foundation

public enum APIError: Error { case http(Int), notAuthenticated, badResponse }

public actor APIClient {
    private let baseURL: URL
    private let deviceToken: String
    private let session: URLSession
    private var jwt: String?

    public init(baseURL: URL, deviceToken: String, session: URLSession = DiscoNet.session) {
        self.baseURL = baseURL
        self.deviceToken = deviceToken
        self.session = session
    }

    private func token() async throws -> String {
        if let jwt { return jwt }
        var req = URLRequest(url: baseURL.appendingPathComponent("auth/device/token"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(["device_token": deviceToken])
        let (data, resp) = try await session.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw APIError.notAuthenticated }
        let out = try JSONDecoder().decode([String: String].self, from: data)
        guard let t = out["token"] else { throw APIError.badResponse }
        jwt = t
        return t
    }

    // Current JWT — for the /sync/events SSE stream that clients open themselves.
    public func authToken() async throws -> String { try await token() }
    // Drop the cached JWT (after a 401 on the SSE stream).
    public func resetAuth() { jwt = nil }

    private func get(path: String, query: [URLQueryItem] = []) async throws -> Data {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent(path),
                                      resolvingAgainstBaseURL: false)!
            if !query.isEmpty { comps.queryItems = query }
            var req = URLRequest(url: comps.url!)
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard code == 200 || code == 206 else { throw APIError.http(code) }
            return data
        }
        throw APIError.notAuthenticated
    }

    public func changes(since: Int64, limit: Int) async throws -> ChangesPage {
        let data = try await get(path: "sync/changes", query: [
            .init(name: "since", value: String(since)),
            .init(name: "limit", value: String(limit)),
        ])
        return try JSONDecoder().decode(ChangesPage.self, from: data)
    }

    public func allChanges(since: Int64, onPage: (ChangesPage) -> Void) async throws -> Int64 {
        var cursor = since
        while true {
            let page = try await changes(since: cursor, limit: 500)
            onPage(page)
            cursor = page.cursor
            if !page.hasMore { return cursor }
        }
    }

    // Stream the download straight to disk: URLSession writes to a temp file, so we
    // never buffer the whole body in memory — essential for large files.
    public func download(nodeID: String, to dst: URL) async throws {
        let url = baseURL.appendingPathComponent("files/\(nodeID)/content")
        for attempt in 0..<2 {
            let tok = try await token()
            var req = URLRequest(url: url)
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            let (tmp, resp) = try await session.download(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; try? FileManager.default.removeItem(at: tmp); continue }
            guard code == 200 || code == 206 else {
                try? FileManager.default.removeItem(at: tmp)
                throw APIError.http(code)
            }
            if FileManager.default.fileExists(atPath: dst.path) { try FileManager.default.removeItem(at: dst) }
            try FileManager.default.moveItem(at: tmp, to: dst)
            return
        }
        throw APIError.notAuthenticated
    }

    // Download content into memory (used when decrypting a vault).
    public func downloadData(nodeID: String) async throws -> Data {
        try await get(path: "files/\(nodeID)/content")
    }

    // The user's UI language (stored on the server).
    public func getLanguage() async throws -> String {
        let data = try await get(path: "me/language")
        struct Out: Decodable { let language: String }
        return try JSONDecoder().decode(Out.self, from: data).language
    }

    public func setLanguage(_ lang: String) async throws {
        let body = try JSONEncoder().encode(["language": lang])
        for attempt in 0..<2 {
            let tok = try await token()
            var req = URLRequest(url: baseURL.appendingPathComponent("me/language"))
            req.httpMethod = "PUT"
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = body
            let (_, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard code == 200 else { throw APIError.http(code) }
            return
        }
        throw APIError.notAuthenticated
    }

    // MARK: - Writes

    // Shared authorized request with a body and a single 401 retry.
    @discardableResult
    private func send(_ method: String, path: String, query: [URLQueryItem] = [],
                      body: Data? = nil, contentType: String? = nil,
                      extraHeaders: [String: String] = [:], ok: Set<Int>) async throws -> Data {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
            if !query.isEmpty { comps.queryItems = query }
            var req = URLRequest(url: comps.url!)
            req.httpMethod = method
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            if let contentType { req.setValue(contentType, forHTTPHeaderField: "Content-Type") }
            for (k, v) in extraHeaders { req.setValue(v, forHTTPHeaderField: k) }
            req.httpBody = body
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard ok.contains(code) else { throw APIError.http(code) }
            return data
        }
        throw APIError.notAuthenticated
    }

    // Upload or replace a file by its relative path, holding the whole thing in memory.
    // Prefer the fileURL overload for anything that came off disk; this one is for content
    // that only exists in memory anyway (vault ciphertext).
    public func uploadFile(relPath: String, data: Data, modifiedAt: Date? = nil) async throws {
        try await send("PUT", path: "sync/file", query: [.init(name: "path", value: relPath)],
                       body: data, contentType: "application/octet-stream",
                       extraHeaders: Self.modifiedAtHeader(modifiedAt), ok: [201])
    }

    // Upload a file straight from disk. URLSession streams it and sets Content-Length
    // itself, so a multi-gigabyte file never has to sit in memory — and a read failure
    // surfaces as a thrown error instead of being swallowed before the call.
    //
    // modifiedAt travels in X-Modified-At so the server dates the content rather than the
    // upload; nil sends no header and leaves the server's own date in place.
    public func uploadFile(relPath: String, fileURL: URL, modifiedAt: Date? = nil) async throws {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent("sync/file"),
                                      resolvingAgainstBaseURL: false)!
            comps.queryItems = [.init(name: "path", value: relPath)]
            var req = URLRequest(url: comps.url!)
            req.httpMethod = "PUT"
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            req.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
            for (k, v) in Self.modifiedAtHeader(modifiedAt) { req.setValue(v, forHTTPHeaderField: k) }
            // Re-reads the file from disk on the retry, so the 401 path stays whole-body.
            let (_, resp) = try await session.upload(for: req, fromFile: fileURL)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard code == 201 else { throw APIError.http(code) }
            return
        }
        throw APIError.notAuthenticated
    }

    // RFC3339 with fractional seconds — what the server parses, and what the Go clients send.
    private static func modifiedAtHeader(_ date: Date?) -> [String: String] {
        guard let date else { return [:] }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        f.timeZone = TimeZone(secondsFromGMT: 0)
        return ["X-Modified-At": f.string(from: date)]
    }

    // The file's own modification date, or nil when the filesystem will not say.
    public static func contentModificationDate(of url: URL) -> Date? {
        (try? url.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
    }

    // MARK: - Folder listing

    /// One entry of a folder listing. Carries the hash, which is what lets a client ask
    /// "is this file already here, byte for byte?" before uploading — the server takes a
    /// same-named upload as a new version of whatever is there.
    public struct FolderEntry: Decodable, Sendable {
        public let id: String
        public let name: String
        public let isDir: Bool
        public let size: Int64?
        public let version: Int64
        public let contentHash: String?
        public let modifiedAt: Date?

        enum CodingKeys: String, CodingKey {
            case id, name, size, version
            case isDir = "is_dir"
            case contentHash = "content_hash"
            case modifiedAt = "modified_at"
        }
    }

    /// Lists a folder (nil = storage root).
    public func listFolder(parentID: String?) async throws -> [FolderEntry] {
        let query = parentID.map { [URLQueryItem(name: "parent_id", value: $0)] } ?? []
        let data = try await get(path: "files", query: query)
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { d in
            let raw = try d.singleValueContainer().decode(String.self)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = f.date(from: raw) { return date }
            f.formatOptions = [.withInternetDateTime]
            return f.date(from: raw) ?? Date(timeIntervalSince1970: 0)
        }
        return try decoder.decode([FolderEntry].self, from: data)
    }

    /// Returns the id of the child folder called `name`, creating it when it is not there.
    /// Idempotent: the common case (it exists) costs one listing and no writes.
    public func ensureFolder(parentID: String?, name: String) async throws -> String {
        if let existing = try await listFolder(parentID: parentID).first(where: { $0.isDir && $0.name == name }) {
            return existing.id
        }
        var body: [String: Any] = ["name": name]
        if let parentID { body["parent_id"] = parentID }
        let data = try await send("POST", path: "files/folder",
                                  body: try JSONSerialization.data(withJSONObject: body),
                                  contentType: "application/json", ok: [200, 201])
        struct Out: Decodable { let id: String }
        return try JSONDecoder().decode(Out.self, from: data).id
    }

    // MARK: - Chunked upload (/upload/*)

    /// An open upload session: where to send chunks, and which one the server wants next.
    public struct UploadSession: Sendable {
        public let uploadID: String
        public let nextChunk: Int
    }

    /// Opens a session. `size` is the file's full length — the server checks the assembled
    /// chunks against it and refuses to publish a short upload, so a transfer that dies
    /// halfway cannot land as a truncated file.
    public func uploadInit(parentID: String?, name: String, size: Int64,
                           modifiedAt: Date?) async throws -> UploadSession {
        var body: [String: Any] = ["name": name, "size": size]
        if let parentID { body["parent_id"] = parentID }
        if let modifiedAt { body["modified_at"] = Self.rfc3339(modifiedAt) }
        let data = try await send("POST", path: "upload/init",
                                  body: try JSONSerialization.data(withJSONObject: body),
                                  contentType: "application/json", ok: [200, 201])
        struct Out: Decodable { let upload_id: String; let next_chunk: Int }
        let out = try JSONDecoder().decode(Out.self, from: data)
        return UploadSession(uploadID: out.upload_id, nextChunk: out.next_chunk)
    }

    /// Sends chunk `index`; returns the next index the server expects. Re-sending an
    /// already-accepted chunk is safe — the server ignores it and answers the same.
    public func uploadChunk(uploadID: String, index: Int, data: Data) async throws -> Int {
        let out = try await send("PUT", path: "upload/\(uploadID)/chunk/\(index)",
                                 body: data, contentType: "application/octet-stream",
                                 ok: [200, 201])
        struct Out: Decodable { let next_chunk: Int }
        return try JSONDecoder().decode(Out.self, from: out).next_chunk
    }

    /// Where to resume from.
    public func uploadStatus(uploadID: String) async throws -> Int {
        let data = try await get(path: "upload/\(uploadID)")
        struct Out: Decodable { let next_chunk: Int }
        return try JSONDecoder().decode(Out.self, from: data).next_chunk
    }

    /// Publishes the assembled file.
    public func uploadComplete(uploadID: String) async throws {
        try await send("POST", path: "upload/\(uploadID)/complete", ok: [200, 201])
    }

    /// Discards an in-progress session and its staged bytes.
    public func uploadAbort(uploadID: String) async throws {
        try await send("DELETE", path: "upload/\(uploadID)", ok: [200, 204])
    }

    private static func rfc3339(_ date: Date) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        f.timeZone = TimeZone(secondsFromGMT: 0)
        return f.string(from: date)
    }

    // MARK: - Folders

    // Create a folder.
    public func createDir(relPath: String) async throws {
        let body = try JSONEncoder().encode(["path": relPath])
        try await send("POST", path: "sync/dir", body: body, contentType: "application/json", ok: [201])
    }

    // Delete a node (file or folder) — moves it to the trash.
    public func delete(nodeID: String) async throws {
        try await send("DELETE", path: "files/\(nodeID)", ok: [204, 200])
    }

    // Rename a node.
    public func rename(nodeID: String, newName: String) async throws {
        let body = try JSONEncoder().encode(["name": newName])
        try await send("PATCH", path: "files/\(nodeID)/rename", body: body, contentType: "application/json", ok: [200])
    }
}
