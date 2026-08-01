import Foundation

/// Auto-upload's own settings.
///
/// Plain UserDefaults rather than the Keychain: none of this is a secret, and the pass has
/// to read it from a background task where a locked Keychain would be a problem.
final class AutoUploadSettings: @unchecked Sendable {

    static let shared = AutoUploadSettings()

    private let defaults: UserDefaults
    init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    /// Master switch. Off until the user turns it on; nothing is uploaded in the meantime.
    var enabled: Bool {
        get { defaults.bool(forKey: "autoUpload.enabled") }
        set { defaults.set(newValue, forKey: "autoUpload.enabled") }
    }

    /// Set once the library has been recorded as pre-existing. Until then a pass would
    /// mistake every old photo for new work.
    var seeded: Bool {
        get { defaults.bool(forKey: "autoUpload.seeded") }
        set { defaults.set(newValue, forKey: "autoUpload.seeded") }
    }

    /// Node id of `/Camera Uploads/<device>`, cached so a pass does not re-resolve it.
    var destID: String? {
        get { defaults.string(forKey: "autoUpload.destID") }
        set { defaults.set(newValue, forKey: "autoUpload.destID") }
    }

    var wifiOnly: Bool {
        get { defaults.object(forKey: "autoUpload.wifiOnly") as? Bool ?? true }
        set { defaults.set(newValue, forKey: "autoUpload.wifiOnly") }
    }

    var chargingOnly: Bool {
        get { defaults.bool(forKey: "autoUpload.chargingOnly") }
        set { defaults.set(newValue, forKey: "autoUpload.chargingOnly") }
    }

    var requireBattery: Bool {
        get { defaults.object(forKey: "autoUpload.requireBattery") as? Bool ?? true }
        set { defaults.set(newValue, forKey: "autoUpload.requireBattery") }
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
