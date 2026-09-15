import Foundation
import Security

// Where Keychain items go. With `accessGroup` set (the macOS app sets it from Info.plist at
// launch) items live in the data-protection keychain under that group, which is the only
// kind of item a File Provider extension of the same team can read. Left nil — iOS, tests —
// items are plain per-app entries as before.
public enum KeychainConfig {
    // Set once at launch, before any Keychain use; nothing writes it afterwards.
    nonisolated(unsafe) public static var accessGroup: String?

    // The attributes every query shares for a given service: class, service, and — when a
    // group is configured — the group plus the data-protection flag that makes macOS honour
    // it (without the flag macOS uses the file-based login keychain, which has no groups).
    static func query(service: String, account: String? = nil, group: String? = accessGroup) -> [String: Any] {
        var q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
        ]
        if let account { q[kSecAttrAccount as String] = account }
        if let group {
            q[kSecAttrAccessGroup as String] = group
            q[kSecUseDataProtectionKeychain as String] = true
        }
        return q
    }
}

public enum KeychainToken {
    // The device token and serverURL are stored as two generic-password Keychain items,
    // accessible only after first unlock and never synced/migrated off this device.
    public static func save(_ value: String, service: String) {
        let base = KeychainConfig.query(service: service)
        SecItemDelete(base as CFDictionary)
        var add = base
        add[kSecValueData as String] = Data(value.utf8)
        add[kSecAttrAccessible as String] = Self.accessibility
        SecItemAdd(add as CFDictionary, nil)
    }

    // The app sits in the menu bar and the File Provider extension serves Finder while
    // the screen is locked, so the pairing must be readable then: after first unlock,
    // not only while unlocked.
    static var accessibility: String { kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String }

    public static func load(service: String) -> String? {
        if let (v, accessible) = read(KeychainConfig.query(service: service)) {
            // An item saved by an earlier build under the stricter class is re-saved once.
            if accessible != Self.accessibility { save(v, service: service) }
            return v
        }
        // An item saved before the app used an access group sits in the old per-app store.
        // Carry it over once, so a pairing made by the previous build survives the upgrade.
        guard KeychainConfig.accessGroup != nil,
              let (legacy, _) = read(KeychainConfig.query(service: service, group: nil)) else { return nil }
        save(legacy, service: service)
        SecItemDelete(KeychainConfig.query(service: service, group: nil) as CFDictionary)
        return legacy
    }

    // The value and the accessibility class it was saved with.
    private static func read(_ base: [String: Any]) -> (String, String)? {
        var query = base
        query[kSecReturnData as String] = true
        query[kSecReturnAttributes as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let item = out as? [String: Any], let data = item[kSecValueData as String] as? Data,
              let value = String(data: data, encoding: .utf8) else { return nil }
        return (value, item[kSecAttrAccessible as String] as? String ?? "")
    }

    public static func delete(service: String) {
        SecItemDelete(KeychainConfig.query(service: service) as CFDictionary)
        if KeychainConfig.accessGroup != nil {
            SecItemDelete(KeychainConfig.query(service: service, group: nil) as CFDictionary)
        }
    }

    public static let tokenService = "org.discodrive.devicetoken"
    public static let serverService = "org.discodrive.serverurl"
}
