import XCTest
import Security
@testable import DiscoKit

/// What a logout leaves behind on Apple platforms: downloads set aside under a dated name,
/// and keychain items keyed by server path rather than by account.
final class LogoutCleanupTests: XCTestCase {
    private var dir: URL!

    override func setUp() {
        super.setUp()
        dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDown() { try? FileManager.default.removeItem(at: dir); super.tearDown() }

    private func makeSetAside(_ date: Date) throws -> URL {
        let url = dir.appendingPathComponent(SetAsideDownloads.name(for: date), isDirectory: true)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        try Data("x".utf8).write(to: url.appendingPathComponent("a.txt"))
        return url
    }

    private func exists(_ url: URL) -> Bool { FileManager.default.fileExists(atPath: url.path) }

    func testOldSetAsidesAreRemovedAndRecentOnesKept() throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let old = try makeSetAside(now.addingTimeInterval(-8 * 86_400))
        let recent = try makeSetAside(now.addingTimeInterval(-2 * 86_400))
        let content = dir.appendingPathComponent("content", isDirectory: true)
        try FileManager.default.createDirectory(at: content, withIntermediateDirectories: true)
        let unrelated = dir.appendingPathComponent("local.sqlite")
        try Data().write(to: unrelated)

        let removed = SetAsideDownloads.prune(in: dir, olderThan: SetAsideDownloads.maxAge, now: now)

        XCTAssertEqual(removed.map(\.lastPathComponent), [old.lastPathComponent])
        XCTAssertFalse(exists(old))
        XCTAssertTrue(exists(recent))
        XCTAssertTrue(exists(content), "the live download folder is never touched")
        XCTAssertTrue(exists(unrelated))
    }

    func testLogoutRemovesEverySetAside() throws {
        let now = Date()
        let a = try makeSetAside(now.addingTimeInterval(-60))
        let b = try makeSetAside(now.addingTimeInterval(-30 * 86_400))
        SetAsideDownloads.prune(in: dir, olderThan: 0, now: now)
        XCTAssertFalse(exists(a))
        XCTAssertFalse(exists(b))
    }

    /// A set-aside whose name carries no readable date falls back to its own timestamp.
    func testUndatedSetAsideUsesItsModificationDate() throws {
        let odd = dir.appendingPathComponent("content.old-whatever", isDirectory: true)
        try FileManager.default.createDirectory(at: odd, withIntermediateDirectories: true)
        SetAsideDownloads.prune(in: dir, olderThan: SetAsideDownloads.maxAge, now: Date())
        XCTAssertTrue(exists(odd), "made just now")
        SetAsideDownloads.prune(in: dir, olderThan: SetAsideDownloads.maxAge, now: Date().addingTimeInterval(8 * 86_400))
        XCTAssertFalse(exists(odd))
    }

    func testNameRoundTripsItsDate() {
        let date = Date(timeIntervalSince1970: 1_800_000_000)
        let name = SetAsideDownloads.name(for: date)
        XCTAssertTrue(name.hasPrefix("content.old-"))
        XCTAssertFalse(name.contains(":"), "a colon is a path separator to Finder")
        XCTAssertEqual(SetAsideDownloads.date(fromName: name), date)
        XCTAssertNil(SetAsideDownloads.date(fromName: "content"))
    }

    /// DAV credentials are stored under a per-server-and-account service name; logout
    /// finds them by prefix, and only them.
    func testDAVCredentialServicesAreFoundByPrefix() {
        let items: [[String: Any]] = [
            [kSecAttrService as String: "org.discodrive.dav.0a1b"],
            [kSecAttrService as String: "org.discodrive.dav.ffee"],
            [kSecAttrService as String: KeychainToken.tokenService],
            [kSecAttrService as String: "org.discodrive.vaultpw", kSecAttrAccount as String: "/Vault"],
            [kSecAttrAccount as String: "no service"],
        ]
        XCTAssertEqual(KeychainToken.services(in: items, withPrefix: KeychainToken.davServicePrefix),
                       ["org.discodrive.dav.0a1b", "org.discodrive.dav.ffee"])
    }
}
