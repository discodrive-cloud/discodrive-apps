import UIKit
import BackgroundTasks

final class PhotoTransferAppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, handleEventsForBackgroundURLSession identifier: String,
                     completionHandler: @escaping () -> Void) {
        AutoUploadService.shared.handleTransferEvents(identifier: identifier, completion: completionHandler)
    }
}

/// Expiration must return background time even if a PhotoKit export is still unwinding.
/// System-owned transfers continue; their separate URLSession event lease acknowledges
/// their callbacks only after the queue checkpoint has been written.
@MainActor
final class PhotoBackgroundLease {
    private var task: BGTask?
    private var timer: Task<Void, Never>?
    var onExpire: (() -> Void)?

    init(task: BGTask) {
        self.task = task
        task.expirationHandler = { @Sendable [weak self] in Task { @MainActor in self?.expire() } }
        if task is BGAppRefreshTask {
            timer = Task { [weak self] in
                do { try await Task.sleep(for: .seconds(20)) } catch { return }
                self?.expire()
            }
        }
    }

    private func expire() { onExpire?(); finish(success: false) }

    func finish(success: Bool) {
        guard let task else { return }
        self.task = nil
        timer?.cancel(); timer = nil
        task.expirationHandler = nil
        onExpire = nil
        task.setTaskCompleted(success: success)
    }
}
