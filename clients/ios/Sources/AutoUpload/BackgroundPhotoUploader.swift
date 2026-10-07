import Foundation
import UIKit
import DiscoKit
import os

/// Owns one account's OS transfer session. Discovery/export does not wait for uploads;
/// URLSession holds file-backed chunk tasks after the application is suspended.
@MainActor
final class BackgroundPhotoUploader: NSObject, URLSessionDataDelegate {
    static let prefix = "org.discodrive.ios.photos."
    private static let log = Logger(subsystem: "org.discodrive.ios", category: "photo-transfer")
    let api: APIClient
    let store: PhotoTransferStore
    private let journal: UploadJournal
    private let settings: AutoUploadSettings
    private let trust: PinningDelegate
    private var session: URLSession!
    private var valid = true
    private var paused = false
    private var ready = false
    private var active: [String: Int] = [:]
    private var restoring = false
    private var preparing: [String: Task<Void, Never>] = [:]
    private var responses: [Int: Data] = [:]
    private var handlingEvents = 0
    private var finishedEvents = false
    private var completion: (() -> Void)?
    private var retryTask: Task<Void, Never>?
    private var retryNotBefore: [String: Date] = [:]
    var onChange: (() -> Void)?

    init(api: APIClient, store: PhotoTransferStore, journal: UploadJournal,
         settings: AutoUploadSettings = .shared, configuration: URLSessionConfiguration? = nil) {
        self.api = api; self.store = store; self.journal = journal; self.settings = settings
        trust = PinningDelegate(pin: api.certificatePin)
        super.init()
        let config = configuration ?? .background(withIdentifier: Self.prefix + api.transferIdentity)
        if configuration == nil {
            config.sessionSendsLaunchEvents = true
            config.isDiscretionary = false
            config.waitsForConnectivity = true
        }
        config.httpMaximumConnectionsPerHost = 2
        config.timeoutIntervalForRequest = 60
        config.timeoutIntervalForResource = 24 * 60 * 60
        session = URLSession(configuration: config, delegate: self, delegateQueue: .main)
    }

    func restore() async {
        guard valid, !restoring else { return }
        restoring = true
        defer { restoring = false }
        ready = false
        let tasks = await session.allTasks
        guard valid else { return }
        do {
            let ids = Set(try store.all(account: api.transferIdentity).map(\.id))
            for task in tasks {
                guard let id = task.taskDescription, ids.contains(id), settings.enabled, !paused, task.state != .canceling, task.state != .completed else {
                    task.cancel(); continue
                }
                if let current = active[id], current != task.taskIdentifier { task.cancel(); continue }
                active[id] = task.taskIdentifier
                if task.state == .suspended { task.resume() }
            }
            ready = true
            pump()
        } catch { Self.log.error("restore failed: \(String(describing: error), privacy: .public)") }
    }

    func queued() throws -> [PhotoTransfer] { try store.all(account: api.transferIdentity) }

    func enqueue(_ job: PhotoTransfer, exportedFile: URL) throws {
        guard valid, settings.enabled, !paused, job.account == api.transferIdentity else { throw CancellationError() }
        guard try !store.contains(account: job.account, assetID: job.assetID, modified: job.modified) else { return }
        try store.stage(job, exportedFile: exportedFile)
        onChange?()
        pump()
    }

    func resume() async {
        guard valid else { return }
        paused = false
        await restore()
    }

    func pause() async {
        paused = true
        retryTask?.cancel(); retryTask = nil
        for task in preparing.values { task.cancel() }
        let tasks = await session.allTasks
        guard paused || !valid else { return }
        for task in tasks { task.cancel() }
        active.removeAll()
    }

    func invalidate() {
        valid = false; paused = true
        retryTask?.cancel()
        for task in preparing.values { task.cancel() }
    }

    func logout() async throws {
        invalidate()
        await pause()
        session.invalidateAndCancel()
        try store.wipe(account: api.transferIdentity)
        finishEventsIfPossible()
    }

    func handleEvents(completion: @escaping () -> Void) {
        self.completion = completion
        finishEventsIfPossible()
    }

    private func finishEventsIfPossible() {
        guard finishedEvents, handlingEvents == 0, preparing.isEmpty, let completion else { return }
        self.completion = nil
        finishedEvents = false
        completion()
    }

    private func pump() {
        guard valid, ready, !paused, settings.enabled else { finishEventsIfPossible(); return }
        do {
            let jobs = try queued()
            for job in jobs where active[job.id] == nil && preparing[job.id] == nil && max(job.retryAt, retryNotBefore[job.id] ?? .distantPast) <= Date() {
                guard active.count + preparing.count < 2 else { break }
                // Power is checked at each chunk boundary. Network waiting is delegated to
                // URLSession, with Wi-Fi/cellular constraints on the individual request.
                let block = Conditions.shared.check(wifiOnly: settings.wifiOnly,
                                                    chargingOnly: settings.chargingOnly,
                                                    requireBattery: settings.requireBattery)
                if block == .notCharging || block == .lowBattery { break }
                preparing[job.id] = Task {
                    await self.advance(job)
                    self.preparing[job.id] = nil
                    self.onChange?()
                    self.pump()
                    self.finishEventsIfPossible()
                }
            }
            retryTask?.cancel()
            if let next = jobs.filter({ active[$0.id] == nil && preparing[$0.id] == nil })
                .map({ max($0.retryAt, retryNotBefore[$0.id] ?? .distantPast) }).filter({ $0 > Date() }).min() {
                retryTask = Task {
                    do { try await Task.sleep(for: .seconds(max(1, next.timeIntervalSinceNow))) }
                    catch { return }
                    self.pump()
                }
            }
        } catch { Self.log.error("queue read failed: \(String(describing: error), privacy: .public)") }
    }

    private func checkActive() throws {
        guard valid, !paused, settings.enabled, !Task.isCancelled else { throw CancellationError() }
    }

    private func checked(_ next: Int, job: PhotoTransfer) throws -> Int {
        guard job.transferChunkSize > 0, job.bytes >= 0 else { throw APIError.badResponse }
        let count = job.bytes / Int64(job.transferChunkSize) + (job.bytes % Int64(job.transferChunkSize) == 0 ? 0 : 1)
        guard next >= 0, Int64(next) <= count else { throw APIError.badResponse }
        return next
    }

    private func advance(_ original: PhotoTransfer) async {
        var job = original
        do {
            try checkActive()
            if !FileManager.default.fileExists(atPath: store.assetURL(job).path) {
                // A missing local export must become discoverable again, not occupy the
                // asset's queue slot forever. The photo library remains authoritative.
                try store.remove(job); return
            }
            if try journal.isKnown(assetID: job.assetID, modified: job.modified) {
                try store.remove(job); return
            }
            if let uploadID = job.uploadID {
                do { job.nextChunk = try checked(try await api.uploadStatus(uploadID: uploadID), job: job) }
                catch APIError.http(404) { job.uploadID = nil; job.nextChunk = 0 }
                try checkActive()
            }
            if job.uploadID == nil {
                let entries: [APIClient.FolderEntry]
                do { entries = try await api.listFolder(parentID: job.parentID) }
                catch APIError.http(404) {
                    let root = try await api.ensureFolder(parentID: nil, name: AutoUploadRunner.destRoot)
                    let parent = try await api.ensureFolder(parentID: root, name: AutoUploadRunner.deviceFolder)
                    try checkActive()
                    job.parentID = parent
                    entries = try await api.listFolder(parentID: parent)
                }
                try checkActive()
                var names = Dictionary(entries.map { ($0.name, $0.isDir ? "" : ($0.contentHash ?? "")) }, uniquingKeysWith: { first, _ in first })
                for reserved in try queued() where reserved.id != job.id && reserved.parentID == job.parentID {
                    if names[reserved.name] == nil { names[reserved.name] = "" }
                }
                switch NameResolver.resolve(job.name, exists: { name in
                    guard let hash = names[name] else { return .absent }
                    return hash == job.sha ? .same : .different
                }) {
                case .alreadyThere:
                    try markSent(job); return
                case .upload(let name): job.name = name
                case .noFreeName: throw APIError.http(409)
                }
                let opened = try await api.uploadInit(parentID: job.parentID, name: job.name, size: job.bytes, modifiedAt: job.created)
                try checkActive()
                job.uploadID = opened.uploadID
                job.nextChunk = try checked(opened.nextChunk, job: job)
            }
            try store.save(job)
            guard let uploadID = job.uploadID else { throw APIError.badResponse }
            let offset = Int64(job.nextChunk) * Int64(job.transferChunkSize)
            if offset >= job.bytes {
                _ = try await api.uploadCompleteResult(uploadID: uploadID)
                try checkActive()
                try markSent(job)
                return
            }
            let body = try store.writeChunk(job)
            var request = try await api.backgroundChunkRequest(uploadID: uploadID, index: job.nextChunk)
            try checkActive()
            request.allowsCellularAccess = !settings.wifiOnly
            request.allowsExpensiveNetworkAccess = !settings.wifiOnly
            let task = session.uploadTask(with: request, fromFile: body)
            task.taskDescription = job.id
            active[job.id] = task.taskIdentifier
            task.resume()
            Self.log.notice("chunk queued: \(job.id, privacy: .public) index=\(job.nextChunk)")
        } catch is CancellationError {
            // Keep the last acknowledged checkpoint. Resume queries the server, including
            // the case where it accepted a chunk whose response we never received.
        } catch {
            guard valid, !paused, settings.enabled else { return }
            if case APIError.http(404) = error { job.uploadID = nil; job.nextChunk = 0 }
            job.deferRetry(String(describing: error))
            retryNotBefore[job.id] = job.retryAt
            do { try store.save(job); retryNotBefore[job.id] = nil } catch { Self.log.error("checkpoint failed: \(String(describing: error), privacy: .public)") }
        }
    }

    private func markSent(_ job: PhotoTransfer) throws {
        // Journal first: a crash between these operations is cleaned up on restore,
        // without republishing the asset or recreating a file deleted on the server.
        try journal.markSent(assetID: job.assetID, modified: job.modified,
                             bytes: job.bytes, sha: job.sha, serverName: job.name)
        try store.remove(job)
        retryNotBefore[job.id] = nil
        Self.log.notice("published: \(job.id, privacy: .public)")
    }

    private func completed(_ task: URLSessionTask, error: Error?) async {
        let data = responses.removeValue(forKey: task.taskIdentifier) ?? Data()
        guard let id = task.taskDescription else { return }
        guard active[id] == task.taskIdentifier || !ready else { return }
        defer {
            if active[id] == task.taskIdentifier { active[id] = nil }
            onChange?(); pump()
        }
        guard valid, !paused, settings.enabled else { return }
        do {
            guard var job = try queued().first(where: { $0.id == id }) else { return }
            // A cancelled old task can finish after pause/resume has opened another
            // server session. Its acknowledgement must not advance the new checkpoint.
            guard let uploadID = job.uploadID,
                  task.originalRequest?.url?.path.hasSuffix("/upload/\(uploadID)/chunk/\(job.nextChunk)") == true else { return }
            let status = (task.response as? HTTPURLResponse)?.statusCode ?? 0
            if error == nil && (status == 200 || status == 201) {
                struct Reply: Decodable { let next_chunk: Int }
                let next = try checked(JSONDecoder().decode(Reply.self, from: data).next_chunk, job: job)
                guard next > job.nextChunk else { throw APIError.badResponse }
                job.nextChunk = next
                job.attempts = 0; job.retryAt = .distantPast; job.lastError = nil
                retryNotBefore[job.id] = nil
            } else if error == nil && status == 413 && job.transferChunkSize > (1 << 20) {
                // Reverse proxies can impose a lower body limit than the server. Start
                // a fresh session with smaller parts; never change an existing offset.
                if let id = job.uploadID { try? await api.uploadAbort(uploadID: id) }
                try checkActive()
                job.chunkSize = max(1 << 20, job.transferChunkSize / 2)
                job.uploadID = nil; job.nextChunk = 0; job.retryAt = .distantPast
                job.lastError = nil
            } else {
                if status == 401 { await api.resetAuth() }
                try checkActive()
                if status == 404 { job.uploadID = nil; job.nextChunk = 0 }
                job.deferRetry(error.map { String(describing: $0) } ?? "HTTP \(status)")
            }
            try store.save(job)
        } catch {
            // Preserve the file and server session even for malformed replies.
            if valid, !paused, var job = try? queued().first(where: { $0.id == id }) {
                job.deferRetry(String(describing: error))
                retryNotBefore[job.id] = job.retryAt
                try? store.save(job)
            }
        }
    }

    // All delegate calls use OperationQueue.main. MainActor.assumeIsolated keeps their
    // ordering (data -> completion -> finishEvents) without unstructured hop races.
    nonisolated func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        MainActor.assumeIsolated {
            if (responses[dataTask.taskIdentifier]?.count ?? 0) + data.count <= 64 * 1024 {
                responses[dataTask.taskIdentifier, default: Data()].append(data)
            } else { dataTask.cancel() }
        }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        MainActor.assumeIsolated {
            handlingEvents += 1
            Task {
                await self.completed(task, error: error)
                self.handlingEvents -= 1
                self.finishEventsIfPossible()
            }
        }
    }

    nonisolated func urlSessionDidFinishEvents(forBackgroundURLSession session: URLSession) {
        MainActor.assumeIsolated { finishedEvents = true; finishEventsIfPossible() }
    }

    nonisolated func urlSession(_ session: URLSession, didReceive challenge: URLAuthenticationChallenge,
                                completionHandler: @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        MainActor.assumeIsolated { trust.urlSession(session, didReceive: challenge, completionHandler: completionHandler) }
    }

    nonisolated func urlSession(_ session: URLSession, task: URLSessionTask,
                                willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                                completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
        MainActor.assumeIsolated { trust.urlSession(session, task: task, willPerformHTTPRedirection: response, newRequest: request, completionHandler: completionHandler) }
    }
}
