import Foundation
import BackgroundTasks
import DiscoKit
import Photos
import UIKit
import os

/// Discovers assets on library changes, foreground entry and system background time.
/// File-backed transfers have a separate persistent owner and can outlive a discovery pass.
@MainActor
final class AutoUploadService: NSObject, ObservableObject {

    static let shared = AutoUploadService()
    private static let log = Logger(subsystem: "org.discodrive.ios", category: "photo-discovery")
    private var backgroundPassOwners: Set<UUID> = []
    private var foregroundLease: UIBackgroundTaskIdentifier = .invalid

    /// Must match BGTaskSchedulerPermittedIdentifiers in Info.plist.
    static let taskID = "org.discodrive.ios.autoupload"
    static let refreshTaskID = "org.discodrive.ios.autoupload.refresh"

    @Published private(set) var queuedTransfers = 0
    @Published private(set) var transferError: String?
    private var transfers: BackgroundPhotoUploader?
    @Published private(set) var running = false
    @Published private(set) var progressText: String?
    @Published private(set) var lastResult: RunResult?

    private let settings = AutoUploadSettings.shared
    private var journal: UploadJournal?
    private var observer: LibraryObserver?
    private var apiProvider: (() -> APIClient?)?
    /// The pass in flight, so it can be stopped. Cancelling is checked between photos: the
    /// one being sent finishes, the rest are dropped.
    private var passTask: Task<RunResult, Never>?
    private var settingsGeneration = 0
    private var passRequested = false
    private var schedulingTask: Task<Void, Never>?
    private var rescheduleIDs: Set<String> = []
    var applicationIsActive: @MainActor () -> Bool = { UIApplication.shared.applicationState == .active }
    var pendingBackgroundRequests: @MainActor () async -> [BGTaskRequest] = {
        await BGTaskScheduler.shared.pendingTaskRequests()
    }
    var submitBackgroundRequest: @MainActor (BGTaskRequest) throws -> Void = {
        try BGTaskScheduler.shared.submit($0)
    }
    /// How a pass waits for the network path to settle. Replaced only by tests, which need
    /// to hold a pass at this suspension point.
    var waitForPath: @MainActor () async -> Void = { await Conditions.shared.waitForPath() }
    /// What stops a pass (network, Wi-Fi, charging). Replaced only by tests.
    var blockingCondition: @MainActor (AutoUploadSettings) -> UploadBlock = { settings in
        Conditions.shared.check(wifiOnly: settings.wifiOnly, chargingOnly: settings.chargingOnly,
                                requireBattery: settings.requireBattery)
    }

    /// Hands the service what it needs from the app: how to reach the server. Called once
    /// the app is paired, and again after re-pairing.
    func configure(apiProvider: @escaping () -> APIClient?) {
        self.apiProvider = apiProvider
    }

    private func uploader(for api: APIClient) throws -> BackgroundPhotoUploader {
        if let transfers, transfers.api.transferIdentity == api.transferIdentity { return transfers }
        if let previous = transfers {
            previous.invalidate()
            Task { try? await previous.logout() }
        }
        let root = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                               appropriateFor: nil, create: true)
            .appendingPathComponent("DiscoDrive/PhotoTransfers", isDirectory: true)
        let transfers = BackgroundPhotoUploader(api: api, store: try PhotoTransferStore(directory: root),
                                               journal: try openJournal())
        transfers.onChange = { [weak self, weak transfers] in
            guard let self, let transfers, self.transfers === transfers else { return }
            let jobs = (try? transfers.queued()) ?? []
            self.queuedTransfers = jobs.count
            self.transferError = jobs.first(where: { $0.lastError != nil })?.lastError
            // Drain a bounded export batch before discovering more archive items.
            if jobs.isEmpty, self.settings.enabled, !self.running {
                Task { await self.runPass() }
            }
        }
        self.transfers = transfers
        Task { await transfers.restore() }
        return transfers
    }

    func restoreTransfers() {
        guard let api = apiProvider?() else { return }
        do { _ = try uploader(for: api) }
        catch { transferError = String(describing: error) }
    }

    func handleTransferEvents(identifier: String, completion: @escaping () -> Void) {
        if let api = apiProvider?(), identifier == BackgroundPhotoUploader.prefix + api.transferIdentity,
           let transfers = try? uploader(for: api) {
            transfers.handleEvents(completion: completion)
            Task { await transfers.restore() }
        } else {
            // A session from a former account must never use the current account's API.
            let session = URLSession(configuration: .background(withIdentifier: identifier))
            Task {
                for task in await session.allTasks { task.cancel() }
                session.invalidateAndCancel()
                completion()
            }
        }
    }

    func openJournal() throws -> UploadJournal {
        if let journal { return journal }
        // The same folder AppState keeps its index in. Asking for "discodrive" instead of
        // "DiscoDrive" looked harmless but failed with an I/O error: the filesystem is
        // case-insensitive, so it is one directory, and creating it under the other spelling
        // is not.
        let dir = try FileManager.default.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                              appropriateFor: nil, create: true)
            .appendingPathComponent("DiscoDrive", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let j = try UploadJournal(path: dir.appendingPathComponent("autoupload.sqlite").path)
        journal = j
        return j
    }

    // MARK: - Enabling

    func setEnabled(_ on: Bool) async {
        settingsGeneration += 1
        let generation = settingsGeneration
        if on {
            // Ask first, store second. Writing the flag up front left the feature marked
            // "on" whenever the permission prompt was refused — or simply left unanswered,
            // since the request does not return until the user decides.
            let status = await PhotoLibrarySource.requestAccess()
            guard settingsGeneration == generation, apiProvider?() != nil else { return }
            guard status == .authorized || status == .limited else {
                settings.enabled = false
                return
            }
            settings.enabled = true
            startObserving()
            scheduleBackgroundPass()
            await runPass()
        } else {
            settings.enabled = false
            // Switching off has to stop what is happening now, not just what would happen
            // next: a back-fill of a few thousand photos would otherwise run to completion.
            stopPass()
            await transfers?.pause()
            stopObserving()
            BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
            BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.refreshTaskID)
        }
    }

    func logout() async {
        settingsGeneration += 1
        settings.enabled = false
        stopObserving()
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
        BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.refreshTaskID)
        stopPass()
        do { try await transfers?.logout() } catch { transferError = String(describing: error) }
        transfers = nil; queuedTransfers = 0
        _ = await passTask?.value
        settings.seeded = false
        settings.destID = nil
        // The next pairing may be another account: what went to this one says nothing
        // about what that one has, and a re-seed must start from an empty journal.
        do { try openJournal().wipe() } catch { lastResult = RunResult(error: String(describing: error)) }
    }

    // MARK: - Passes

    /// Runs a pass now, if the conditions allow it.
    @discardableResult
    func runPass() async -> RunResult {
        guard settings.enabled, !Task.isCancelled else { return RunResult() }
        if let task = passTask {
            if task.isCancelled {
                _ = await task.value
                return await runPass()
            }
            // A new photo or foreground transition during a pass needs another scan.
            // Join the owner so a background caller does not report completion early.
            passRequested = true
            return await task.value
        }
        let generation = settingsGeneration
        Self.log.notice("discovery started")
        running = true
        let task = Task { @MainActor in
            defer { self.passTask = nil; self.running = false; self.progressText = nil }
            var result = RunResult()
            repeat {
                self.passRequested = false
                let pass = await self.performPass(generation: generation)
                result.uploaded += pass.uploaded
                result.queued += pass.queued
                result.skipped += pass.skipped
                result.deferred += pass.deferred
                result.blocked = pass.blocked
                if let error = pass.error { result.error = error }
                self.lastResult = result
                Self.log.notice("discovery pass finished: queued=\(pass.queued) deferred=\(pass.deferred) blocked=\(String(describing: pass.blocked), privacy: .public) error=\(pass.error ?? "none", privacy: .public)")
            } while self.passRequested && self.settings.enabled &&
                    self.settingsGeneration == generation && !Task.isCancelled
            return result
        }
        passTask = task
        return await task.value
    }

    private func performPass(generation: Int) async -> RunResult {
        guard let api = apiProvider?() else { return RunResult(error: "not paired") }
        // Give the path monitor a moment before believing it: at launch it reports nothing
        // for a beat, and calling that "no network" is how a pass silently did nothing.
        await waitForPath()
        // Logout or switch-off while waiting: the journal may be wiped and `api` belongs to
        // the account being left. Seeding or uploading now would undo the logout.
        guard settings.enabled, settingsGeneration == generation, !Task.isCancelled else { return RunResult() }
        let blocked = blockingCondition(settings)
        guard blocked == .none else {
            var r = RunResult(); r.blocked = blocked
            lastResult = r
            return r
        }

        do {
            let journal = try openJournal()
            let transfers = try uploader(for: api)
            await transfers.resume()
            guard settings.enabled, settingsGeneration == generation, !Task.isCancelled else { return RunResult() }
            let runner = AutoUploadRunner(api: api, journal: journal, settings: settings, transfers: transfers)
            try Task.checkCancellation()
            try await runner.discover()
            try Task.checkCancellation()
            return await runner.runOnce(progress: { [weak self] done, total, name in
                Task { @MainActor in self?.progressText = "\(done + 1)/\(total) · \(name)" }
            }, isCancelled: { Task.isCancelled })
        } catch {
            var r = RunResult(); r.error = String(describing: error)
            lastResult = r
            return r
        }
    }

    /// Stops the pass in flight. The photo being sent finishes — cutting a transfer
    /// mid-chunk would only leave the server to garbage-collect it — and the queue is
    /// dropped after it.
    func stopPass() {
        passRequested = false
        passTask?.cancel()
        if let transfers { Task { await transfers.pause() } }
    }

    /// Turns the photos that were marked "already there" back into work, then runs a pass.
    ///
    /// Seeding is what stops a phone from dumping years of pictures the moment the feature
    /// is switched on — but wanting that archive on your own server is the whole point of
    /// running one, so it has to be reachable on purpose. Returns how many photos were
    /// queued.
    @discardableResult
    func uploadExistingPhotos() async -> Int {
        guard let journal = try? openJournal() else { return 0 }
        let queued = (try? journal.unseed()) ?? 0
        guard queued > 0 else { return 0 }
        await runPass()
        return queued
    }

    /// How many photos are currently sitting as "already there".
    func preexistingCount() -> Int {
        (try? openJournal().counts().skipped) ?? 0
    }

    // MARK: - Library observer

    private func startObserving() {
        guard observer == nil else { return }
        let o = LibraryObserver { [weak self] in
            Task { @MainActor in await self?.runPass() }
        }
        PHPhotoLibrary.shared().register(o)
        observer = o
    }

    private func stopObserving() {
        if let observer { PHPhotoLibrary.shared().unregisterChangeObserver(observer) }
        observer = nil
    }

    /// Reconcile on launch and foreground entry: PhotoKit callbacks are not a durable queue.
    func resumeIfEnabled() {
        guard settings.enabled else { return }
        startObserving()
        scheduleBackgroundPass()
        Task { await runPass() }
    }

    func conditionsChanged() {
        Task {
            await transfers?.pause()
            scheduleBackgroundPass()
            if settings.enabled { await runPass() }
        }
    }

    func enteredBackground() {
        guard settings.enabled, foregroundLease == .invalid else { return }
        foregroundLease = UIApplication.shared.beginBackgroundTask(withName: "photo-discovery") { [weak self] in
            guard let self else { return }
            if !self.applicationIsActive(), self.backgroundPassOwners.isEmpty {
                self.passRequested = false
                self.passTask?.cancel()
            }
            self.endForegroundLease()
        }
        Task {
            _ = await runPass()
            endForegroundLease()
        }
    }

    private func endForegroundLease() {
        guard foregroundLease != .invalid else { return }
        let lease = foregroundLease
        foregroundLease = .invalid
        UIApplication.shared.endBackgroundTask(lease)
    }

    // MARK: - Background task

    /// Registers the handler. Must run before the app finishes launching.
    func registerBackgroundTask() {
        // The handler inherits MainActor isolation. A nil queue lets BGTaskScheduler
        // invoke it on its worker queue and traps before the inner Task can hop actors.
        for identifier in [Self.taskID, Self.refreshTaskID] {
            BGTaskScheduler.shared.register(forTaskWithIdentifier: identifier, using: .main) { task in
                Task { @MainActor in
                    self.scheduleBackgroundPass(replacing: identifier)
                    let owner = self.retainBackgroundPass()
                    let lease = PhotoBackgroundLease(task: task)
                    let work = Task { await self.runPass() }
                    lease.onExpire = {
                        work.cancel()
                        self.releaseBackgroundPass(owner, expired: true)
                    }
                    let result = await work.value
                    self.releaseBackgroundPass(owner, expired: false)
                    lease.finish(success: !work.isCancelled && result.error == nil && result.blocked == .none && result.deferred == 0)
                }
            }
        }
    }

    /// Refresh and processing can share a discovery pass. Expiration of one system
    /// task must not cancel work still covered by the other task's execution time.
    func retainBackgroundPass() -> UUID {
        let id = UUID()
        backgroundPassOwners.insert(id)
        return id
    }

    func releaseBackgroundPass(_ id: UUID, expired: Bool) {
        guard backgroundPassOwners.remove(id) != nil else { return }
        if expired, backgroundPassOwners.isEmpty, foregroundLease == .invalid,
           !self.applicationIsActive() {
            passRequested = false
            passTask?.cancel()
        }
    }

    /// Asks for background time. iOS decides when — usually when the phone is idle and
    /// charging — so this is "eventually", never "in twenty minutes".
    func scheduleBackgroundPass(replacing identifier: String? = nil) {
        if let identifier { rescheduleIDs.insert(identifier) }
        guard settings.enabled, schedulingTask == nil else { return }
        schedulingTask = Task { @MainActor in
            defer { self.schedulingTask = nil }
            let pending = await self.pendingBackgroundRequests()
            guard self.settings.enabled else { return }
            let replacing = self.rescheduleIDs
            self.rescheduleIDs.removeAll()
            // Foreground reconciliation must not push an already scheduled request
            // fifteen minutes into the future each time the user opens the app.
            let existing = pending.first { $0.identifier == Self.taskID } as? BGProcessingTaskRequest
            if replacing.contains(Self.taskID) || existing == nil || existing?.requiresExternalPower != self.settings.chargingOnly {
                let request = BGProcessingTaskRequest(identifier: Self.taskID)
                request.requiresNetworkConnectivity = true
                request.requiresExternalPower = self.settings.chargingOnly
                request.earliestBeginDate = replacing.contains(Self.taskID) ? Date(timeIntervalSinceNow: 15 * 60) : (existing?.earliestBeginDate ?? Date(timeIntervalSinceNow: 15 * 60))
                do { try self.submitBackgroundRequest(request) }
                catch { self.transferError = String(describing: error) }
            }
            if replacing.contains(Self.refreshTaskID) || !pending.contains(where: { $0.identifier == Self.refreshTaskID }) {
                let request = BGAppRefreshTaskRequest(identifier: Self.refreshTaskID)
                request.earliestBeginDate = Date(timeIntervalSinceNow: 5 * 60)
                do { try self.submitBackgroundRequest(request) }
                catch { self.transferError = String(describing: error) }
            }
        }
    }
}

/// PhotoKit's observer protocol is Objective-C, so it needs a small class of its own.
private final class LibraryObserver: NSObject, PHPhotoLibraryChangeObserver {
    private let onChange: @Sendable () -> Void
    /// A burst of photos produces a burst of callbacks; one pass covers them all.
    private var pending: DispatchWorkItem?
    private let lock = NSLock()

    init(onChange: @escaping @Sendable () -> Void) { self.onChange = onChange }

    func photoLibraryDidChange(_ changeInstance: PHChange) {
        lock.lock()
        defer { lock.unlock() }
        pending?.cancel()
        let item = DispatchWorkItem { [onChange] in onChange() }
        pending = item
        DispatchQueue.main.asyncAfter(deadline: .now() + 5, execute: item)
    }
}
