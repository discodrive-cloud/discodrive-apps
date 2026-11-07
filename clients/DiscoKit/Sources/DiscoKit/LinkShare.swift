import Foundation

// "Share → DiscoDrive": what the share extensions do with a link, kept free of UIKit so it
// is tested here. The extension asks the server once, briefly; what it shows depends only
// on the answer, and links that could not be sent wait in a small outbox for the next try.
public enum LinkShare {
    public enum Outcome: Equatable, Sendable {
        case saved      // the server has it
        case queued     // no answer in time: kept in the outbox, sent later
        case signIn     // this device is not (or no longer) paired
        case rejected   // the server will never take it (not a web link, internal address)
    }

    /// How long the extension waits for the server before it queues the link.
    public static let answerTimeout: Duration = .milliseconds(1500)

    /// The first http(s) link in shared text ("Посмотрите https://… !"). Only one written
    /// with its scheme: the detector also turns a bare "threads.net" into a link, and that
    /// is a mention, not the post being shared.
    public static func firstWebURL(in text: String) -> URL? {
        guard let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue) else { return nil }
        let ns = text as NSString
        for match in detector.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let written = ns.substring(with: match.range).lowercased()
            guard written.hasPrefix("http://") || written.hasPrefix("https://"),
                  let url = match.url, url.host != nil else { continue }
            return url
        }
        return nil
    }

    /// nil status: the request failed or timed out.
    public static func outcome(status: Int?) -> Outcome {
        guard let status else { return .queued }
        switch status {
        case 200..<300: return .saved
        case 401, 403: return .signIn
        case 400, 413, 422: return .rejected
        default: return .queued   // 429, 5xx, a proxy's error page: worth another try
        }
    }

    public static func message(for outcome: Outcome, language: String) -> String {
        let key: String
        switch outcome {
        case .saved, .queued: key = "saved"
        case .signIn: key = "signIn"
        case .rejected: key = "rejected"
        }
        let lang = String(language.prefix(2)).lowercased()
        return messages[key]?[lang] ?? messages[key]!["en"]!
    }

    // Formal address everywhere ("войдите", not "войди"); the product is "DiscoDrive".
    static let messages: [String: [String: String]] = [
        "saved": ["en": "✓ Saved to DiscoDrive", "ru": "✓ Сохранено в DiscoDrive", "uk": "✓ Збережено в DiscoDrive",
                  "de": "✓ In DiscoDrive gespeichert", "fr": "✓ Enregistré dans DiscoDrive",
                  "es": "✓ Guardado en DiscoDrive", "sr": "✓ Сачувано у DiscoDrive"],
        "signIn": ["en": "Sign in to DiscoDrive first", "ru": "Сначала войдите в DiscoDrive", "uk": "Спершу увійдіть у DiscoDrive",
                   "de": "Melden Sie sich zuerst bei DiscoDrive an", "fr": "Connectez-vous d’abord à DiscoDrive",
                   "es": "Inicie sesión primero en DiscoDrive", "sr": "Прво се пријавите у DiscoDrive"],
        "rejected": ["en": "This link cannot be saved", "ru": "Эту ссылку нельзя сохранить", "uk": "Це посилання не можна зберегти",
                     "de": "Dieser Link kann nicht gespeichert werden", "fr": "Ce lien ne peut pas être enregistré",
                     "es": "Este enlace no se puede guardar", "sr": "Овај линк не може да се сачува"],
    ]

    /// Sends what waits in the outbox, oldest first. Links the server took or will never
    /// take leave the outbox; the first one that cannot get through stops the flush, so a
    /// dead network costs one request, not one per link.
    public static func flush(outbox: LinkOutbox, client: APIClient) async {
        for entry in outbox.pending() {
            if Task.isCancelled { return }
            let status: Int?
            do { status = try await client.saveLink(url: entry.url, title: entry.title, sharedAt: entry.added) }
            catch APIError.notAuthenticated { status = 401 }
            catch { status = nil }
            switch outcome(status: status) {
            case .saved, .rejected, .signIn: outbox.remove([entry.id])
            case .queued: return
            }
        }
    }
}

/// Links waiting to be sent, in a JSON file in the App Group container shared by the app
/// and its share extension. Only URLs and titles: the credentials stay in the Keychain.
public final class LinkOutbox: @unchecked Sendable {
    public struct Entry: Codable, Equatable, Sendable {
        public let id: UUID
        public let url: URL
        public let title: String?
        public let added: Date
    }

    public static let capacity = 200

    private let file: URL
    private let lock = NSLock()

    public init(directory: URL) {
        file = directory.appendingPathComponent("link-outbox.json")
    }

    /// The outbox in the App Group container, nil when the group is not available.
    public convenience init?(appGroup: String) {
        guard let dir = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup) else { return nil }
        self.init(directory: dir)
    }

    public func enqueue(_ url: URL, title: String?) {
        mutate { entries in
            entries.append(Entry(id: UUID(), url: url, title: title, added: Date()))
            if entries.count > Self.capacity { entries.removeFirst(entries.count - Self.capacity) }
        }
    }

    public func pending() -> [Entry] {
        lock.lock(); defer { lock.unlock() }
        return load()
    }

    public func remove(_ ids: [UUID]) {
        let drop = Set(ids)
        mutate { $0.removeAll { drop.contains($0.id) } }
    }

    private func mutate(_ change: (inout [Entry]) -> Void) {
        lock.lock(); defer { lock.unlock() }
        var entries = load()
        change(&entries)
        guard let data = try? JSONEncoder().encode(entries) else { return }
        try? data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    private func load() -> [Entry] {
        guard let data = try? Data(contentsOf: file) else { return [] }
        return (try? JSONDecoder().decode([Entry].self, from: data)) ?? []
    }
}

extension APIClient {
    /// POST /me/links. Returns the HTTP status (the caller decides what it means); a
    /// device the server no longer knows throws APIError.notAuthenticated.
    ///
    /// sharedAt: when a queued link was shared. The server keeps an archived link archived
    /// when the user archived it after that, so a resent copy cannot undo it.
    public func saveLink(url: URL, title: String?, sharedAt: Date? = nil) async throws -> Int {
        var payload: [String: String] = ["url": url.absoluteString]
        if let title, !title.isEmpty { payload["title"] = title }
        if let sharedAt { payload["shared_at"] = ISO8601DateFormatter().string(from: sharedAt) }
        let body = try JSONEncoder().encode(payload)
        do {
            try await send("POST", path: "me/links", body: body, contentType: "application/json", ok: Set(200..<300))
            return 200
        } catch APIError.http(let code) {
            return code
        }
    }
}
