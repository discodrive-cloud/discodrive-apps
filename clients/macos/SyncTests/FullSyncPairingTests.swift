import XCTest

final class FullSyncPairingTests: XCTestCase {
    func testRePairRenamesWholeFolderEvenWhenCredentialIsReused() throws {
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let folder = root.appendingPathComponent("Sync")
        let backups = root.appendingPathComponent("Sync Backups")
        let suite = "test.fullsync." + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { try? fm.removeItem(at: root); defaults.removePersistentDomain(forName: suite) }
        try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults)
        try fm.createDirectory(at: folder.appendingPathComponent(".obsidian"), withIntermediateDirectories: true)
        let note = folder.appendingPathComponent("note.md")
        try Data("old bytes".utf8).write(to: note)
        try Data("old settings".utf8).write(to: folder.appendingPathComponent(".obsidian/settings.json"))
        // Ordinary restart keeps the established mirror and offline user edits.
        XCTAssertNil(try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults))
        let inode = try fm.attributesOfItem(atPath: folder.path)[.systemFileNumber] as? NSNumber
        // Logout invalidates the marker even if an old state.db survives or a token is reused.
        FullSyncPairing.invalidate(defaults: defaults)
        let backup = try XCTUnwrap(FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults))
        XCTAssertEqual(try fm.contentsOfDirectory(atPath: folder.path), [])
        XCTAssertEqual(try fm.attributesOfItem(atPath: backup.path)[.systemFileNumber] as? NSNumber, inode)
        XCTAssertNotEqual(try fm.attributesOfItem(atPath: folder.path)[.systemFileNumber] as? NSNumber, inode)
        XCTAssertEqual(try String(contentsOf: backup.appendingPathComponent("note.md"), encoding: .utf8), "old bytes")
        XCTAssertTrue(fm.fileExists(atPath: backup.appendingPathComponent(".obsidian/settings.json").path))
        try Data("new server bytes".utf8).write(to: note)
        XCTAssertNil(try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults))
        XCTAssertEqual(try String(contentsOf: note, encoding: .utf8), "new server bytes")
        // Different credentials also force a clean mirror without trusting a logout hook.
        XCTAssertNotNil(try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "another pairing", defaults: defaults))
        XCTAssertEqual(try fm.contentsOfDirectory(atPath: folder.path), [])
    }

    func testBackupFailureDoesNotAuthorizeThePairing() throws {
        let fm = FileManager.default
        let root = fm.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let folder = root.appendingPathComponent("Sync"), backups = root.appendingPathComponent("backups")
        let suite = "test.fullsync." + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { try? fm.removeItem(at: root); defaults.removePersistentDomain(forName: suite) }
        try fm.createDirectory(at: folder, withIntermediateDirectories: true)
        try Data("old".utf8).write(to: folder.appendingPathComponent("note"))
        try Data().write(to: backups)
        XCTAssertThrowsError(try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults))
        XCTAssertEqual(try String(contentsOf: folder.appendingPathComponent("note"), encoding: .utf8), "old")
        try fm.removeItem(at: backups)
        XCTAssertNotNil(try FullSyncPairing.prepare(folder: folder, backups: backups, identity: "pairing", defaults: defaults))
        XCTAssertEqual(try fm.contentsOfDirectory(atPath: folder.path), [])
    }
}
