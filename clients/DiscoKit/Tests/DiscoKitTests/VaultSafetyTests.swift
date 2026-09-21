import XCTest
@testable import DiscoKit

final class VaultSafetyTests: XCTestCase {
    func testLongNamesCannotChangeTypeDuringRenameMoveOrCreate() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let name = String(repeating: "n", count: 200)
        for sourceIsDir in [false, true] {
            for moving in [false, true] {
                let io = VaultInFinderTests.MemoryVaultIO()
                let target = moving ? try await v.createFolder(name: "target", parentDirID: "", sink: io) : ""
                if sourceIsDir {
                    try await v.createFolder(name: "source", parentDirID: "", sink: io)
                    try await v.addFile(name: name, data: Data("theirs".utf8), parentDirID: target, sink: io)
                } else {
                    try await v.addFile(name: "source", data: Data("ours".utf8), parentDirID: "", sink: io)
                    try await v.createFolder(name: name, parentDirID: target, sink: io)
                }
                io.unseen = [v.entryPath(name: name, parentDirID: target)]
                let listed = try await v.listEntries(dirID: "", source: io)
                let entry = try XCTUnwrap(listed.first { $0.name == "source" })
                let before = io.files
                do {
                    if moving { try await v.moveEntry(entry, from: "", to: target, as: name, source: io, sink: io) }
                    else { try await v.renameEntry(entry, to: name, parentDirID: "", source: io, sink: io) }
                    XCTFail("a destination of another type must refuse the write")
                } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken(name)) }
                XCTAssertEqual(io.files, before, "neither source nor destination was changed")
                do {
                    if sourceIsDir { try await v.createFolder(name: name, parentDirID: target, sink: io) }
                    else { try await v.addFile(name: name, data: Data(), parentDirID: target, sink: io, createOnly: true) }
                    XCTFail("creating an entry must also refuse the other type")
                } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken(name)) }
                XCTAssertEqual(io.files, before)
            }
        }
    }

    func testConflictArtifactDoesNotBlockOtherEntries() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let io = VaultInFinderTests.MemoryVaultIO()
        try await v.addFile(name: "visible.txt", data: Data("kept".utf8), parentDirID: "", sink: io)
        let path = v.entryPath(name: "visible.txt", parentDirID: "")
        let conflict = String(path.dropLast(4)) + " (conflict, desktop, 2026-09-21 12-00-00).c9r"
        io.files[conflict] = io.files[path]
        let listed = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(listed.map(\.name), ["visible.txt"])
        let bytes = try await v.decryptFile(at: try XCTUnwrap(listed.first?.contentPath), source: io)
        XCTAssertEqual(bytes, Data("kept".utf8))
        XCTAssertNotNil(io.files[conflict], "listing must not delete untracked artifacts")
    }

    func testConflictCleanupSurvivesFailureAndRestart() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let path = dir.appendingPathComponent("index.sqlite").path
        do {
            let index = try IndexStore(path: path)
            try index.queueVaultConflictRemoval(nodeID: "conflict")
            let cleanup = VaultConflictCleanup(index: index)
            try await cleanup.retry { _ in throw URLError(.networkConnectionLost) }
            XCTAssertEqual(try index.pendingVaultConflictRemovals(), ["conflict"])
        }
        let reopened = try IndexStore(path: path)
        var deleted: [String] = []
        try await VaultConflictCleanup(index: reopened).retry { deleted.append($0) }
        XCTAssertEqual(deleted, ["conflict"])
        XCTAssertEqual(try reopened.pendingVaultConflictRemovals(), [])
        try reopened.queueVaultConflictRemoval(nodeID: "already-gone")
        try await VaultConflictCleanup(index: reopened).retry { _ in throw APIError.http(404) }
        XCTAssertEqual(try reopened.pendingVaultConflictRemovals(), [])
    }

    func testLocalSinkRefusesToOverwriteTheNameClaim() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let io = LocalVaultIO(root: root)
        try await io.createFile("wrapper/name.c9s", Data("first".utf8))
        do {
            try await io.createFile("wrapper/name.c9s", Data("second".utf8))
            XCTFail("create must preserve the existing file")
        } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken("wrapper/name.c9s")) }
        let kept = try await io.read("wrapper/name.c9s")
        XCTAssertEqual(kept, Data("first".utf8))
    }
}
