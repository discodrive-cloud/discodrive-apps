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
    private var path: NWPath?

    private init() {
        monitor.pathUpdateHandler = { [weak self] p in self?.path = p }
        monitor.start(queue: queue)
        DispatchQueue.main.async { UIDevice.current.isBatteryMonitoringEnabled = true }
    }

    func check(wifiOnly: Bool, chargingOnly: Bool, requireBattery: Bool) -> UploadBlock {
        guard let path, path.status == .satisfied else { return .noNetwork }
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
