import Foundation

/// Auto-upload's own settings.
///
/// Plain UserDefaults rather than the Keychain: none of this is a secret, and the pass has
/// to read it from a background task where a locked Keychain would be a problem.
final class AutoUploadSettings: @unchecked Sendable {

    static let shared = AutoUploadSettings()

    private let defaults: UserDefaults
    init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    /// UserDefaults writes to disk on its own schedule, and a process that dies before it
    /// gets round to it loses the change. Switching auto-upload on and having the app go
    /// away — killed by a test runner, or by iOS reclaiming memory — left the feature off
    /// with a seeded journal, which reads as "it silently forgot".
    private func set(_ value: Any?, _ key: String) {
        defaults.set(value, forKey: key)
        defaults.synchronize()
    }

    /// Master switch. Off until the user turns it on; nothing is uploaded in the meantime.
    var enabled: Bool {
        get { defaults.bool(forKey: "autoUpload.enabled") }
        set { set(newValue, "autoUpload.enabled") }
    }

    /// Set once the library has been recorded as pre-existing. Until then a pass would
    /// mistake every old photo for new work.
    var seeded: Bool {
        get { defaults.bool(forKey: "autoUpload.seeded") }
        set { set(newValue, "autoUpload.seeded") }
    }

    /// Node id of `/DeviceUploads/<device>`, cached so a pass does not re-resolve it.
    var destID: String? {
        get { defaults.string(forKey: "autoUpload.destID") }
        set { set(newValue, "autoUpload.destID") }
    }

    var wifiOnly: Bool {
        get { defaults.object(forKey: "autoUpload.wifiOnly") as? Bool ?? true }
        set { set(newValue, "autoUpload.wifiOnly") }
    }

    var chargingOnly: Bool {
        get { defaults.bool(forKey: "autoUpload.chargingOnly") }
        set { set(newValue, "autoUpload.chargingOnly") }
    }

    var requireBattery: Bool {
        get { defaults.object(forKey: "autoUpload.requireBattery") as? Bool ?? true }
        set { set(newValue, "autoUpload.requireBattery") }
    }

    /// Wipes everything auto-upload remembers except the journal, which belongs to the
    /// library rather than to the pairing.
    func reset() {
        for key in ["autoUpload.enabled", "autoUpload.seeded", "autoUpload.destID",
                    "autoUpload.wifiOnly", "autoUpload.chargingOnly", "autoUpload.requireBattery"] {
            defaults.removeObject(forKey: key)
        }
    }
}
