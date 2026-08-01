import Foundation
import BackgroundTasks
import DiscoKit
import Photos
import UIKit

/// Drives auto-upload: owns the journal, runs passes, and wires the two things that can
/// start one — the photo library changing, and iOS handing the app background time.
///
/// What iOS allows here is genuinely weaker than Android, and the UI says so rather than
/// pretending otherwise: there is no equivalent of a foreground service, background time is
/// granted at the system's discretion, and nothing runs at all while the app is force-quit.
@MainActor
final class AutoUploadService: NSObject, ObservableObject {

    static let shared = AutoUploadService()

    /// Must match BGTaskSchedulerPermittedIdentifiers in Info.plist.
    static let taskID = "org.discodrive.ios.autoupload"

    @Published private(set) var running = false
    @Published private(set) var progressText: String?
    @Published private(set) var lastResult: RunResult?

    private let settings = AutoUploadSettings.shared
    private var journal: UploadJournal?
    private var observer: LibraryObserver?
    private var apiProvider: (() -> APIClient?)?

    /// Hands the service what it needs from the app: how to reach the server. Called once
    /// the app is paired, and again after re-pairing.
    func configure(apiProvider: @escaping () -> APIClient?) {
        self.apiProvider = apiProvider
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
        if on {
            // Ask first, store second. Writing the flag up front left the feature marked
            // "on" whenever the permission prompt was refused — or simply left unanswered,
            // since the request does not return until the user decides.
            let status = await PhotoLibrarySource.requestAccess()
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
            stopObserving()
            BGTaskScheduler.shared.cancel(taskRequestWithIdentifier: Self.taskID)
        }
    }

    // MARK: - Passes

    /// Runs a pass now, if the conditions allow it.
    @discardableResult
    func runPass() async -> RunResult {
        guard settings.enabled, !running else { return RunResult() }
        guard let api = apiProvider?() else {
            return RunResult(error: "not paired")
        }
        // Give the path monitor a moment before believing it: at launch it reports nothing
        // for a beat, and calling that "no network" is how a pass silently did nothing.
        await Conditions.shared.waitForPath()
        let blocked = Conditions.shared.check(wifiOnly: settings.wifiOnly,
                                              chargingOnly: settings.chargingOnly,
                                              requireBattery: settings.requireBattery)
        guard blocked == .none else {
            var r = RunResult(); r.blocked = blocked
            lastResult = r
            return r
        }

        running = true
        defer { running = false; progressText = nil }
        do {
            let journal = try openJournal()
            let runner = AutoUploadRunner(api: api, journal: journal, settings: settings)
            _ = try await runner.seedIfNeeded()
            let result = await runner.runOnce(progress: { [weak self] done, total, name in
                Task { @MainActor in self?.progressText = "\(done + 1)/\(total) · \(name)" }
            })
            lastResult = result
            return result
        } catch {
            var r = RunResult(); r.error = String(describing: error)
            lastResult = r
            return r
        }
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

    /// Called at launch so a paired, enabled app starts watching without a visit to settings.
    func resumeIfEnabled() {
        guard settings.enabled else { return }
        startObserving()
        scheduleBackgroundPass()
    }

    // MARK: - Background task

    /// Registers the handler. Must run before the app finishes launching.
    func registerBackgroundTask() {
        BGTaskScheduler.shared.register(forTaskWithIdentifier: Self.taskID, using: nil) { task in
            guard let task = task as? BGProcessingTask else { return }
            Task { @MainActor in
                // Always leave a successor behind: BGProcessingTask is one-shot.
                self.scheduleBackgroundPass()
                let work = Task { await self.runPass() }
                task.expirationHandler = { work.cancel() }
                _ = await work.value
                task.setTaskCompleted(success: true)
            }
        }
    }

    /// Asks for background time. iOS decides when — usually when the phone is idle and
    /// charging — so this is "eventually", never "in twenty minutes".
    func scheduleBackgroundPass() {
        let request = BGProcessingTaskRequest(identifier: Self.taskID)
        request.requiresNetworkConnectivity = true
        request.requiresExternalPower = settings.chargingOnly
        request.earliestBeginDate = Date(timeIntervalSinceNow: 15 * 60)
        try? BGTaskScheduler.shared.submit(request)
    }
}

/// PhotoKit's observer protocol is Objective-C, so it needs a small class of its own.
private final class LibraryObserver: NSObject, PHPhotoLibraryChangeObserver {
    private let onChange: @Sendable () -> Void
    /// A burst of photos produces a burst of callbacks; one pass covers them all.
    private var pending: DispatchWorkItem?

    init(onChange: @escaping @Sendable () -> Void) { self.onChange = onChange }

    func photoLibraryDidChange(_ changeInstance: PHChange) {
        pending?.cancel()
        let item = DispatchWorkItem { [onChange] in onChange() }
        pending = item
        DispatchQueue.main.asyncAfter(deadline: .now() + 5, execute: item)
    }
}
