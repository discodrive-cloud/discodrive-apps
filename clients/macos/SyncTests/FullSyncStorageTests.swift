import XCTest

final class FullSyncStorageTests: XCTestCase {
    func testPreservesFilesHiddenDirectoriesAndSelectedFolderIdentity() throws {
        let temp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: temp) }
        let root = temp.appendingPathComponent("mirror")
        let state = temp.appendingPathComponent("state")
        try FileManager.default.createDirectory(at: root.appendingPathComponent(".obsidian"), withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: state, withIntermediateDirectories: true)
        try Data("notes".utf8).write(to: root.appendingPathComponent("note.md"))
        try Data("settings".utf8).write(to: root.appendingPathComponent(".obsidian/settings.json"))
        let before = try FileManager.default.attributesOfItem(atPath: root.path)[.systemFileNumber] as? NSNumber
        let backup = try XCTUnwrap(FullSyncStorage.prepare(folder: root, state: state, backups: temp.appendingPathComponent("backups")))
        XCTAssertEqual(try String(contentsOf: backup.appendingPathComponent("note.md"), encoding: .utf8), "notes")
        XCTAssertEqual(try String(contentsOf: backup.appendingPathComponent(".obsidian/settings.json"), encoding: .utf8), "settings")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path), [])
        XCTAssertEqual(try FileManager.default.attributesOfItem(atPath: root.path)[.systemFileNumber] as? NSNumber, before)
        // A restarted app retains newly edited mirror files, including after an incomplete pull.
        try Data().write(to: state.appendingPathComponent("state.db"))
        try Data("new".utf8).write(to: root.appendingPathComponent("note.md"))
        XCTAssertEqual(try FullSyncStorage.prepare(folder: root, state: state, backups: temp.appendingPathComponent("backups"))?.path, backup.path)
        XCTAssertEqual(try String(contentsOf: root.appendingPathComponent("note.md"), encoding: .utf8), "new")
    }

    func testBackupFailureDoesNotMarkFolderPreparedOrDeleteFiles() throws {
        let temp = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: temp) }
        let root = temp.appendingPathComponent("mirror")
        let state = temp.appendingPathComponent("state")
        let backups = temp.appendingPathComponent("backups")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: state, withIntermediateDirectories: true)
        try Data("keep".utf8).write(to: root.appendingPathComponent("a"))
        try Data().write(to: backups)
        XCTAssertThrowsError(try FullSyncStorage.prepare(folder: root, state: state, backups: backups))
        XCTAssertFalse(FileManager.default.fileExists(atPath: state.appendingPathComponent("prepared").path))
        XCTAssertEqual(try String(contentsOf: root.appendingPathComponent("a"), encoding: .utf8), "keep")
    }
}
