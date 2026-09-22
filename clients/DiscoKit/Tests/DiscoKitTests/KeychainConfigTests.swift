import XCTest
import Security
@testable import DiscoKit

final class KeychainConfigTests: XCTestCase {
    func testSharedReadDistinguishesMissingCredentialsFromKeychainFailure() throws {
        XCTAssertNil(try KeychainToken.sharedValue(status: errSecItemNotFound, data: nil))
        XCTAssertEqual(try KeychainToken.sharedValue(status: errSecSuccess, data: Data("token".utf8)), "token")
        for status in [errSecInteractionNotAllowed, errSecMissingEntitlement, errSecNotAvailable] {
            XCTAssertThrowsError(try KeychainToken.sharedValue(status: status, data: nil)) {
                XCTAssertEqual(($0 as NSError).domain, NSOSStatusErrorDomain)
                XCTAssertEqual(($0 as NSError).code, Int(status))
            }
        }
        XCTAssertThrowsError(try KeychainToken.sharedValue(status: errSecSuccess, data: nil))
    }

    func testQueryWithoutGroupIsPlainPerAppItem() {
        let q = KeychainConfig.query(service: "svc", group: nil)
        XCTAssertEqual(q[kSecAttrService as String] as? String, "svc")
        XCTAssertNil(q[kSecAttrAccessGroup as String])
        XCTAssertNil(q[kSecUseDataProtectionKeychain as String])
        XCTAssertNil(q[kSecAttrAccount as String])
    }

    func testQueryWithGroupTargetsTheDataProtectionKeychain() {
        // On macOS an access group only means something in the data-protection keychain;
        // without that flag the item would land in the login keychain where the extension
        // cannot see it.
        let q = KeychainConfig.query(service: "svc", account: "acct", group: "TEAM.org.discodrive")
        XCTAssertEqual(q[kSecAttrAccessGroup as String] as? String, "TEAM.org.discodrive")
        XCTAssertEqual(q[kSecUseDataProtectionKeychain as String] as? Bool, true)
        XCTAssertEqual(q[kSecAttrAccount as String] as? String, "acct")
    }

    func testDefaultGroupComesFromConfig() {
        let saved = KeychainConfig.accessGroup
        defer { KeychainConfig.accessGroup = saved }
        KeychainConfig.accessGroup = "TEAM.grp"
        XCTAssertEqual(KeychainConfig.query(service: "svc")[kSecAttrAccessGroup as String] as? String, "TEAM.grp")
        KeychainConfig.accessGroup = nil
        XCTAssertNil(KeychainConfig.query(service: "svc")[kSecAttrAccessGroup as String])
    }
}
