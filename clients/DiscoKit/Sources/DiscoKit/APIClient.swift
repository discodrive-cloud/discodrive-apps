import Foundation

public enum APIError: Error {
    case http(Int), notAuthenticated, badResponse
    /// The server has no such node: purged, in the trash, or never there. Only a request
    /// addressed to a node by id reports it, and only for the server's own 404
    /// {"error":"not found"} — an expired upload session, a blob missing on disk or a
    /// proxy's page stay `.http(404)`.
    case nodeNotFound
    /// Purge / empty trash kept a trashed folder that still holds an item not in the trash
    /// (the server's 409). Everything else asked for was removed.
    case trashBlocked

    static func isNodeNotFound(status: Int, body: Data) -> Bool {
        guard status == 404,
              let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any] else { return false }
        return obj["error"] as? String == "not found"
    }
}

public actor APIClient {
    /// Declares, on every request, which protocol capabilities this client understands —
    /// so the server can change behavior that depends on client support without breaking
    /// older releases still in the field.
    ///
    /// "delete-by-id": this client applies change-feed deletes by node id and ignores
    /// deletes for ids it does not have indexed (never falls back to deleting by path).
    /// Without this header the server withholds feed deletes of permanently purged nodes,
    /// because a released client that deletes by path could wipe a live file that was
    /// simply moved.
    public static let featuresHeaderField = "X-Discodrive-Features"
    public static let featuresHeaderValue = "delete-by-id"

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
        req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
        req.httpBody = try JSONEncoder().encode(["device_token": deviceToken])
        let (data, resp) = try await session.data(for: req)
        guard let code = (resp as? HTTPURLResponse)?.statusCode else { throw APIError.badResponse }
        if code == 401 || code == 403 { throw APIError.notAuthenticated }
        guard code == 200 else { throw APIError.http(code) }
        let out = try JSONDecoder().decode([String: String].self, from: data)
        guard let t = out["token"] else { throw APIError.badResponse }
        jwt = t
        return t
    }

    // Current JWT — for the /sync/events SSE stream that clients open themselves.
    public func authToken() async throws -> String { try await token() }
    // Drop the cached JWT (after a 401 on the SSE stream).
    public func resetAuth() { jwt = nil }

    // node: the path addresses one node by id, so the server's "not found" means the node
    // is gone (APIError.nodeNotFound).
    private func get(path: String, query: [URLQueryItem] = [], node: Bool = false) async throws -> Data {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent(path),
                                      resolvingAgainstBaseURL: false)!
            if !query.isEmpty { comps.queryItems = query }
            var req = URLRequest(url: comps.url!)
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            if node && APIError.isNodeNotFound(status: code, body: data) { throw APIError.nodeNotFound }
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
            req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
            let (tmp, resp) = try await session.download(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; try? FileManager.default.removeItem(at: tmp); continue }
            guard code == 200 || code == 206 else {
                let gone = code == 404 && APIError.isNodeNotFound(status: code, body: (try? Data(contentsOf: tmp)) ?? Data())
                try? FileManager.default.removeItem(at: tmp)
                throw gone ? APIError.nodeNotFound : APIError.http(code)
            }
            if FileManager.default.fileExists(atPath: dst.path) { try FileManager.default.removeItem(at: dst) }
            try FileManager.default.moveItem(at: tmp, to: dst)
            return
        }
        throw APIError.notAuthenticated
    }

    // Download content into memory (used when decrypting a vault).
    public func downloadData(nodeID: String) async throws -> Data {
        try await get(path: "files/\(nodeID)/content", node: true)
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
            req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
            req.httpBody = body
            let (_, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard code == 200 else { throw APIError.http(code) }
            return
        }
        throw APIError.notAuthenticated
    }

    public struct TrashItem: Decodable, Identifiable, Sendable {
        public let id: String
        public let name: String
        public let is_dir: Bool
        public let size: Int64?
        public let deleted_at: String?
    }

    public struct FileVersion: Decodable, Identifiable, Sendable {
        public let version: Int64
        public let size: Int64?
        public let is_conflict_loser: Bool
        public var id: Int64 { version }
    }

    public func trash() async throws -> [TrashItem] {
        try JSONDecoder().decode([TrashItem].self, from: await get(path: "files/trash"))
    }

    public func undelete(id: String) async throws {
        try await send("POST", path: "files/\(id)/undelete", ok: [200])
    }

    public func purge(id: String) async throws {
        do { try await send("DELETE", path: "files/\(id)/purge", ok: [204]) }
        catch APIError.http(409) { throw APIError.trashBlocked }
    }

    public func emptyTrash() async throws {
        do { try await send("DELETE", path: "files/trash", ok: [204]) }
        catch APIError.http(409) { throw APIError.trashBlocked }
    }

    public func versions(nodeID: String) async throws -> [FileVersion] {
        try JSONDecoder().decode([FileVersion].self, from: await get(path: "files/\(nodeID)/versions", node: true))
    }

    public func restoreVersion(nodeID: String, version: Int64) async throws {
        try await send("POST", path: "files/\(nodeID)/restore",
                       body: JSONEncoder().encode(["version": version]), contentType: "application/json", ok: [200], node: true)
    }

    public struct Share: Decodable, Identifiable, Sendable {
        public let share_id: String
        public let kind: String
        public let email: String?
        public let access: String
        public let expires_at: String?
        public var id: String { share_id }
    }

    public struct ShareResult: Decodable, Sendable {
        public let share_id: String
        public let token: String?
    }

    public func shares(nodeID: String) async throws -> [Share] {
        try JSONDecoder().decode([Share].self, from: await get(path: "files/\(nodeID)/shares", node: true))
    }

    public func share(nodeID: String, email: String?, expiresInSeconds: Int?) async throws -> ShareResult {
        var payload: [String: Any] = ["access": "read"]
        if let email { payload["email"] = email } else { payload["link"] = true }
        if let expiresInSeconds { payload["expires_in_seconds"] = expiresInSeconds }
        let data = try await send("POST", path: "files/\(nodeID)/share", body: JSONSerialization.data(withJSONObject: payload), contentType: "application/json", ok: [201], node: true)
        return try JSONDecoder().decode(ShareResult.self, from: data)
    }

    public func revokeShare(id: String) async throws {
        try await send("DELETE", path: "shares/\(id)", ok: [204])
    }

    public func shareURL(token: String) -> URL { baseURL.appendingPathComponent("s").appendingPathComponent(token) }

    public struct DAVAccess: Decodable, Sendable {
        public let caldav: Bool
        public let carddav: Bool
    }
    public struct DAVAccount: Decodable, Sendable {
        public let id: String
        public let email: String
    }
    public struct DAVCredential: Codable, Sendable {
        public let id: String
        public let password: String
    }
    public func davAccess() async throws -> DAVAccess {
        try JSONDecoder().decode(DAVAccess.self, from: await get(path: "me/access"))
    }
    public func davAccount() async throws -> DAVAccount {
        try JSONDecoder().decode(DAVAccount.self, from: await get(path: "me"))
    }
    public func createDAVPassword(name: String) async throws -> DAVCredential {
        let data = try await send("POST", path: "devices/webdav", body: JSONEncoder().encode(["name": name]), contentType: "application/json", ok: [201])
        return try JSONDecoder().decode(DAVCredential.self, from: data)
    }
    /// Ends this device on the server, so its token stops working. Call on sign-out while
    /// the token is still known; a device the server already rejects counts as revoked.
    public func revokeThisDevice() async throws {
        let session: String
        do { session = try await token() } catch APIError.notAuthenticated { return }
        guard let id = Self.deviceID(fromJWT: session) else { throw APIError.badResponse }
        do { try await send("DELETE", path: "devices/\(id)", ok: [200, 204, 404]) }
        catch APIError.notAuthenticated { return }
    }

    /// The device id claim ("did") of our own session token.
    static func deviceID(fromJWT jwt: String) -> String? {
        let parts = jwt.split(separator: ".")
        guard parts.count == 3 else { return nil }
        var b64 = parts[1].replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        b64 += String(repeating: "=", count: (4 - b64.count % 4) % 4)
        guard let data = Data(base64Encoded: b64),
              let claims = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let id = claims["did"] as? String, !id.isEmpty else { return nil }
        return id
    }

    public func revokeDAVPassword(id: String) async throws {
        try await send("DELETE", path: "devices/\(id)", ok: [200, 204, 404])
    }
    public func appleProfile(installationID: String, calendars: Bool, contacts: Bool) async throws -> URL {
        let body = try JSONSerialization.data(withJSONObject: ["server_url": baseURL.absoluteString, "installation_id": installationID, "calendars": calendars, "contacts": contacts])
        let data = try await send("POST", path: "me/apple-profile", body: body, contentType: "application/json", ok: [201])
        struct Response: Decodable { let download_path: String }
        let path = try JSONDecoder().decode(Response.self, from: data).download_path
        guard path.hasPrefix("/apple-profile/"), !path.contains(".."),
              var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else { throw APIError.badResponse }
        components.path = path
        components.query = nil
        components.fragment = nil
        guard let url = components.url else { throw APIError.badResponse }
        return url
    }

    public func appleEnrollmentAvailable() async throws -> Bool {
        struct Response: Decodable { let enabled: Bool }
        return try JSONDecoder().decode(Response.self, from: await get(path: "me/apple-enrollment")).enabled
    }

    public func appleEnrollment(installationID: String, calendars: Bool, contacts: Bool, credential: DAVCredential) async throws -> URL {
        guard baseURL.scheme?.lowercased() == "https" else { throw APIError.badResponse }
        let body = try JSONSerialization.data(withJSONObject: [
            "server_url": baseURL.absoluteString, "installation_id": installationID,
            "calendars": calendars, "contacts": contacts,
            "device_id": credential.id, "password": credential.password,
        ])
        let data = try await send("POST", path: "me/apple-enrollment", body: body, contentType: "application/json", ok: [201])
        struct Response: Decodable { let download_path: String }
        let path = try JSONDecoder().decode(Response.self, from: data).download_path
        let parts = path.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 4, parts[0].isEmpty, parts[1] == "apple-enrollment",
              parts[2].count == 64, parts[2].allSatisfy({ "0123456789abcdef".contains($0) }),
              parts[3] == "DiscoDrive.mobileconfig",
              var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false) else { throw APIError.badResponse }
        components.path = path; components.query = nil; components.fragment = nil
        guard let url = components.url else { throw APIError.badResponse }
        return url
    }

    // MARK: - Writes

    // Shared authorized request with a body and a single 401 retry.
    @discardableResult
    private func send(_ method: String, path: String, query: [URLQueryItem] = [],
                      body: Data? = nil, contentType: String? = nil,
                      extraHeaders: [String: String] = [:], ok: Set<Int>, node: Bool = false) async throws -> Data {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent(path), resolvingAgainstBaseURL: false)!
            if !query.isEmpty { comps.queryItems = query }
            var req = URLRequest(url: comps.url!)
            req.httpMethod = method
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
            if let contentType { req.setValue(contentType, forHTTPHeaderField: "Content-Type") }
            for (k, v) in extraHeaders { req.setValue(v, forHTTPHeaderField: k) }
            req.httpBody = body
            let (data, resp) = try await session.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            if node && APIError.isNodeNotFound(status: code, body: data) { throw APIError.nodeNotFound }
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
    /// What the server did with an upload: the node it landed as, and whether the server
    /// kept its own newer version and filed this one as a conflict copy.
    public struct UploadOutcome: Sendable, Equatable {
        public let nodeID: String
        public let version: Int64
        public let conflicted: Bool
    }

    public func uploadFile(relPath: String, fileURL: URL, modifiedAt: Date? = nil) async throws {
        _ = try await uploadFile(relPath: relPath, fileURL: fileURL, modifiedAt: modifiedAt, baseVersion: nil)
    }

    /// The PUT with the version the file was edited from: a newer server version becomes a
    /// conflict copy rather than being overwritten, and the outcome says so.
    @discardableResult
    public func uploadFile(relPath: String, fileURL: URL, modifiedAt: Date?, baseVersion: Int64?) async throws -> UploadOutcome {
        for attempt in 0..<2 {
            let tok = try await token()
            var comps = URLComponents(url: baseURL.appendingPathComponent("sync/file"),
                                      resolvingAgainstBaseURL: false)!
            comps.queryItems = [.init(name: "path", value: relPath)]
            var req = URLRequest(url: comps.url!)
            req.httpMethod = "PUT"
            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
            req.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
            req.setValue(Self.featuresHeaderValue, forHTTPHeaderField: Self.featuresHeaderField)
            for (k, v) in Self.modifiedAtHeader(modifiedAt) { req.setValue(v, forHTTPHeaderField: k) }
            if let baseVersion { req.setValue(String(baseVersion), forHTTPHeaderField: "X-Base-Version") }
            // Re-reads the file from disk on the retry, so the 401 path stays whole-body.
            let (data, resp) = try await session.upload(for: req, fromFile: fileURL)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            if code == 401 && attempt == 0 { jwt = nil; continue }
            guard code == 201 else { throw APIError.http(code) }
            return Self.outcome(from: data)
        }
        throw APIError.notAuthenticated
    }

    static func outcome(from data: Data) -> UploadOutcome {
        struct Out: Decodable {
            struct N: Decodable { let id: String; let version: Int64? }
            let node: N?; let conflicted: Bool?
        }
        let out = try? JSONDecoder().decode(Out.self, from: data)
        return UploadOutcome(nodeID: out?.node?.id ?? "", version: out?.node?.version ?? 0, conflicted: out?.conflicted ?? false)
    }

    /// The one way to upload a file that lives on disk. A file that fits in one chunk goes
    /// up in a single PUT; anything longer takes the resumable chunked protocol, so a
    /// connection dropped halfway through a large video continues from the server's
    /// next_chunk instead of starting over.
    ///
    /// `relPath` is the server path (folder + name); both transports address the file by it.
    @discardableResult
    public func upload(fileURL: URL, relPath: String, modifiedAt: Date?, baseVersion: Int64? = nil,
                       chunkSize: Int = ChunkedUploader.defaultChunkSize,
                       progress: (@Sendable (Int64, Int64) -> Void)? = nil) async throws -> UploadOutcome {
        let size = (try? fileURL.resourceValues(forKeys: [.fileSizeKey]))?.fileSize ?? 0
        if size > chunkSize {
            return try await ChunkedUploader(api: self, chunkSize: chunkSize)
                .upload(fileURL: fileURL, to: .path(relPath, baseVersion: baseVersion), modifiedAt: modifiedAt, progress: progress)
        }
        let out = try await uploadFile(relPath: relPath, fileURL: fileURL, modifiedAt: modifiedAt, baseVersion: baseVersion)
        progress?(Int64(size), Int64(size))
        return out
    }

    /// The same for content that only exists in memory (vault ciphertext): past one chunk
    /// it is staged in a temporary file and sent through the resumable protocol.
    public func upload(data: Data, relPath: String, modifiedAt: Date? = nil,
                       chunkSize: Int = ChunkedUploader.defaultChunkSize) async throws {
        guard data.count > chunkSize else {
            try await uploadFile(relPath: relPath, data: data, modifiedAt: modifiedAt)
            return
        }
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("ddk-upload-\(UUID().uuidString)")
        try data.write(to: tmp)
        defer { try? FileManager.default.removeItem(at: tmp) }
        try await ChunkedUploader(api: self, chunkSize: chunkSize)
            .upload(fileURL: tmp, to: .path(relPath), modifiedAt: modifiedAt)
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

    /// Opens a session addressed by server path, the way `PUT /sync/file` is: the folder
    /// chain is created on the way, and `baseVersion` — the version the file was edited
    /// from, nil when unknown — makes a newer server version a conflict copy rather than
    /// an overwrite. Needs a server that knows `path` on `/upload/init`.
    public func uploadInit(path: String, baseVersion: Int64? = nil, size: Int64,
                           modifiedAt: Date?) async throws -> UploadSession {
        var body: [String: Any] = ["path": path, "size": size]
        if let baseVersion { body["base_version"] = baseVersion }
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
        _ = try await uploadCompleteResult(uploadID: uploadID)
    }

    /// Publishes the assembled file and says what the server did with it.
    public func uploadCompleteResult(uploadID: String) async throws -> UploadOutcome {
        Self.outcome(from: try await send("POST", path: "upload/\(uploadID)/complete", ok: [200, 201]))
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
        try await send("DELETE", path: "files/\(nodeID)", ok: [204, 200], node: true)
    }

    // Move a node under another folder (nil = the storage root).
    public func move(nodeID: String, newParentID: String?) async throws {
        let body = try JSONSerialization.data(withJSONObject: ["parent_id": newParentID as Any? ?? NSNull()])
        try await send("PATCH", path: "files/\(nodeID)/move", body: body, contentType: "application/json", ok: [200], node: true)
    }

    // Whether the caller's node still exists (GET /files/{id}, owner's nodes only). Settles
    // which node a move's "not found" was about: the moved node or the destination.
    public func nodeExists(nodeID: String) async throws -> Bool {
        do { _ = try await get(path: "files/\(nodeID)", node: true); return true }
        catch APIError.nodeNotFound { return false }
    }

    // Rename a node.
    public func rename(nodeID: String, newName: String) async throws {
        let body = try JSONEncoder().encode(["name": newName])
        try await send("PATCH", path: "files/\(nodeID)/rename", body: body, contentType: "application/json", ok: [200], node: true)
    }
}
