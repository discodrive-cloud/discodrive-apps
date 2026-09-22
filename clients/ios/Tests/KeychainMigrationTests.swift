import XCTest
import Security
import DiscoKit

final class KeychainMigrationTests: XCTestCase {
    func testLegacyPairingMovesToSharedGroupWithoutDeletingTheNewCopy() throws {
        let shared = try XCTUnwrap(Bundle.main.object(forInfoDictionaryKey: "DiscoDriveKeychainGroup") as? String)
        let team = try XCTUnwrap(shared.split(separator: ".").first)
        let legacy = String(team) + "." + (Bundle.main.bundleIdentifier ?? "")
        let service = "discodrive.test.migration." + UUID().uuidString
        func query(_ group: String) -> [String: Any] {
            [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
             kSecAttrAccessGroup as String: group]
        }
        var old = query(legacy)
        old[kSecValueData as String] = Data("old-device-token".utf8)
        XCTAssertEqual(SecItemAdd(old as CFDictionary, nil), errSecSuccess)
        defer {
            SecItemDelete(query(legacy) as CFDictionary)
            SecItemDelete(query(shared) as CFDictionary)
        }
        XCTAssertEqual(KeychainConfig.accessGroup, shared)
        XCTAssertEqual(KeychainToken.load(service: service), "old-device-token")
        XCTAssertEqual(try KeychainToken.loadShared(service: service), "old-device-token")
        XCTAssertEqual(SecItemCopyMatching(query(legacy) as CFDictionary, nil), errSecItemNotFound)
        XCTAssertTrue(KeychainToken.save("updated-device-token", service: service))
        XCTAssertEqual(try KeychainToken.loadShared(service: service), "updated-device-token")
    }
}
