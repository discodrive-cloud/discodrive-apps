import Foundation
import Network
import UIKit

/// Why a pass is not running right now — shown in the UI so "nothing happens" is explainable.
enum UploadBlock: Sendable, Equatable {
    case none
    case noNetwork
    case needsWiFi
    case notCharging
    case lowBattery
}

/// The network and power rules that gate a pass.
///
/// iOS has no roaming flag an app can read, so unlike Android there are three switches, not
/// four: `isExpensive` already covers cellular (and personal hotspots), which is what the
/// "Wi-Fi only" setting is really about.
final class Conditions: @unchecked Sendable {

    static let shared = Conditions()

    /// Below this an upload competes with the user's remaining battery.
    static let minBatteryPercent = 20

    private let monitor = NWPathMonitor()
    private let queue = DispatchQueue(label: "org.discodrive.conditions")
    private var observed: NWPath?

    private init() {
        monitor.pathUpdateHandler = { [weak self] p in self?.observed = p }
        monitor.start(queue: queue)
        DispatchQueue.main.async { UIDevice.current.isBatteryMonitoringEnabled = true }
    }

    /// The handler fires asynchronously, and `currentPath` is not usable either until the
    /// monitor has settled: a pass started from a freshly opened app read "no network" and
    /// did nothing — the very moment a user is most likely to press Upload now.
    private var path: NWPath { observed ?? monitor.currentPath }

    /// Waits briefly for the monitor to report a usable path. Callers that can await do so
    /// before checking, which turns a false "no network" into a short pause.
    func waitForPath(timeout: TimeInterval = 5) async {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if path.status == .satisfied { return }
            try? await Task.sleep(nanoseconds: 200_000_000)
        }
    }

    func check(wifiOnly: Bool, chargingOnly: Bool, requireBattery: Bool) -> UploadBlock {
        let path = self.path
        guard path.status == .satisfied else { return .noNetwork }
        if wifiOnly && path.isExpensive { return .needsWiFi }

        let state = UIDevice.current.batteryState
        let plugged = state == .charging || state == .full
        if chargingOnly && !plugged { return .notCharging }
        if requireBattery && !plugged {
            let level = UIDevice.current.batteryLevel   // -1 when unknown
            if level >= 0 && Int(level * 100) < Self.minBatteryPercent { return .lowBattery }
        }
        return .none
    }
}
