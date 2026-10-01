import Foundation
import Security

// Where Keychain items go. With `accessGroup` set (the macOS app sets it from Info.plist at
// launch) items live in the data-protection keychain under that group, which is the only
// kind of item a File Provider extension of the same team can read. Left nil — tests —
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
    @discardableResult
    public static func save(_ value: String, service: String) -> Bool {
        let base = KeychainConfig.query(service: service)
        let attributes: [String: Any] = [kSecValueData as String: Data(value.utf8),
                                        kSecAttrAccessible as String: Self.accessibility]
        let updated = SecItemUpdate(base as CFDictionary, attributes as CFDictionary)
        if updated == errSecSuccess { return true }
        guard updated == errSecItemNotFound else { return false }
        var add = base
        add.merge(attributes) { _, new in new }
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    // The app sits in the menu bar and the File Provider extension serves Finder while
    // the screen is locked, so the pairing must be readable then: after first unlock,
    // not only while unlocked.
    static var accessibility: String { kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String }

    public static func load(service: String) -> String? {
        if let (v, accessible, _) = read(KeychainConfig.query(service: service)) {
            // An item saved by an earlier build under the stricter class is re-saved once.
            if accessible != Self.accessibility { save(v, service: service) }
            return v
        }
        // An item saved before the app used an access group sits in the old per-app store.
        // Carry it over once, so a pairing made by the previous build survives the upgrade.
        guard KeychainConfig.accessGroup != nil,
              let (legacy, _, legacyGroup) = read(KeychainConfig.query(service: service, group: nil)) else { return nil }
        guard save(legacy, service: service) else { return legacy }
        // On iOS an unspecified group searches every accessible group, including the
        // shared item just written. Delete only the legacy item's actual group.
        if let legacyGroup, legacyGroup != KeychainConfig.accessGroup {
            SecItemDelete(KeychainConfig.query(service: service, group: legacyGroup) as CFDictionary)
        }
        #if os(macOS)
        if legacyGroup == nil { SecItemDelete(KeychainConfig.query(service: service, group: nil) as CFDictionary) }
        #endif
        return legacy
    }

    // Extensions must not migrate/delete credentials or hide access failures as a logout.
    public static func loadShared(service: String) throws -> String? {
        var query = KeychainConfig.query(service: service)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        return try sharedValue(status: status, data: result as? Data)
    }

    static func sharedValue(status: OSStatus, data: Data?) throws -> String? {
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess else { throw NSError(domain: NSOSStatusErrorDomain, code: Int(status)) }
        guard let data, let value = String(data: data, encoding: .utf8) else {
            throw NSError(domain: NSOSStatusErrorDomain, code: Int(errSecDecode))
        }
        return value
    }

    // The value and the accessibility class it was saved with.
    private static func read(_ base: [String: Any]) -> (String, String, String?)? {
        var query = base
        query[kSecReturnData as String] = true
        query[kSecReturnAttributes as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess,
              let item = out as? [String: Any], let data = item[kSecValueData as String] as? Data,
              let value = String(data: data, encoding: .utf8) else { return nil }
        return (value, item[kSecAttrAccessible as String] as? String ?? "", item[kSecAttrAccessGroup as String] as? String)
    }

    public static func delete(service: String) {
        SecItemDelete(KeychainConfig.query(service: service) as CFDictionary)
        if KeychainConfig.accessGroup != nil {
            SecItemDelete(KeychainConfig.query(service: service, group: nil) as CFDictionary)
        }
    }

    public static let tokenService = "org.discodrive.devicetoken"
    public static let serverService = "org.discodrive.serverurl"
    /// Fingerprint of the self-signed certificate the user trusted at pairing, if any.
    public static let pinService = "org.discodrive.serverpin"
    /// DAV credentials are stored per server and account under this prefix plus a hash.
    public static let davServicePrefix = "org.discodrive.dav."

    /// Deletes every item whose service starts with `prefix` — the DAV credentials, whose
    /// full service name needs the account id, which a logout does not have to hand.
    public static func deleteAll(servicePrefix prefix: String) {
        var groups: [String?] = [KeychainConfig.accessGroup]
        if KeychainConfig.accessGroup != nil { groups.append(nil) }   // items from before the group
        for group in groups {
            var q: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecReturnAttributes as String: true,
                                    kSecMatchLimit as String: kSecMatchLimitAll]
            if let group {
                q[kSecAttrAccessGroup as String] = group
                q[kSecUseDataProtectionKeychain as String] = true
            }
            var out: CFTypeRef?
            guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
                  let items = out as? [[String: Any]] else { continue }
            for service in services(in: items, withPrefix: prefix) {
                SecItemDelete(KeychainConfig.query(service: service, group: group) as CFDictionary)
            }
        }
    }

    static func services(in items: [[String: Any]], withPrefix prefix: String) -> [String] {
        Array(Set(items.compactMap { $0[kSecAttrService as String] as? String }.filter { $0.hasPrefix(prefix) })).sorted()
    }
}
