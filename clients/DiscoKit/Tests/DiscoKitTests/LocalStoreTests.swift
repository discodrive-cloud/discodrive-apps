import XCTest
import GRDB
@testable import DiscoKit

final class LocalStoreTests: XCTestCase {
    func makeStore() throws -> (LocalStore, URL) {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let store = try LocalStore(dbQueue: DatabaseQueue(), contentDir: dir)
        return (store, dir)
    }

    func tmpFile(_ s: String) throws -> URL {
        let u = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try Data(s.utf8).write(to: u); return u
    }

    func testStoreCachedThenStatusAndStale() throws {
        let (store, dir) = try makeStore()
        XCTAssertEqual(try store.status(nodeID: "n1", serverVersion: 1), .none)
        let f = try tmpFile("hello")
        try store.store(nodeID: "n1", version: 1, from: f, pinned: false, relPath: "/Папка/a.txt")
        XCTAssertEqual(try store.status(nodeID: "n1", serverVersion: 1), .cached)
        XCTAssertEqual(try store.status(nodeID: "n1", serverVersion: 2), .stale) // server version bumped
        let url = store.localURL(nodeID: "n1")!
        XCTAssertEqual(try String(contentsOf: url, encoding: .utf8), "hello")
        // mirrors the tree with the proper file name
        XCTAssertEqual(url, dir.appendingPathComponent("Папка/a.txt"))
        XCTAssertEqual(url.lastPathComponent, "a.txt")
    }

    func testRemovingTheLastFileRemovesTheFoldersItWasIn() throws {
        // folder1/folder2/folder3/file1 downloaded alone: removing it leaves no empty
        // folders behind, a .DS_Store Finder dropped on the way does not count as content.
        let (store, dir) = try makeStore()
        try store.store(nodeID: "f1", version: 1, from: tmpFile("x"), pinned: false, relPath: "folder1/folder2/folder3/file1")
        try Data().write(to: dir.appendingPathComponent("folder1/folder2/.DS_Store"))
        try store.remove(nodeID: "f1")
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("folder1").path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.path), "the content root itself stays")
    }

    func testRemovingOneFileKeepsAFolderThatStillHoldsAnother() throws {
        let (store, dir) = try makeStore()
        try store.store(nodeID: "a", version: 1, from: tmpFile("x"), pinned: false, relPath: "folder1/folder2/a")
        try store.store(nodeID: "b", version: 1, from: tmpFile("y"), pinned: false, relPath: "folder1/b")
        try store.remove(nodeID: "a")
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("folder1/folder2").path))
        XCTAssertTrue(FileManager.default.fileExists(atPath: dir.appendingPathComponent("folder1/b").path))
        XCTAssertEqual(try store.status(nodeID: "b", serverVersion: 1), .cached)
    }

    func testPinAndEvictKeepsPinned() throws {
        let (store, _) = try makeStore()
        try store.store(nodeID: "c", version: 1, from: try tmpFile("c"), pinned: false, relPath: "c.txt")
        try store.store(nodeID: "p", version: 1, from: try tmpFile("p"), pinned: true, relPath: "sub/p.txt")
        try store.pin(nodeID: "c")
        try store.unpin(nodeID: "c") // back to cached
        try store.evictCached()
        XCTAssertEqual(try store.status(nodeID: "c", serverVersion: 1), .none)   // evicted
        XCTAssertEqual(try store.status(nodeID: "p", serverVersion: 1), .pinned) // retained
    }
    // MARK: - Review fixes: pins, missing files, import vs. cache

    func testANewerVersionStoredOverAPinnedCopyStaysPinned() throws {
        // Opening a pinned file after it changed on the server downloads with pinned: false;
        // "keep local" must survive that, or freeing space removes it.
        let (store, _) = try makeStore()
        try store.store(nodeID: "p", version: 1, from: try tmpFile("v1"), pinned: true, relPath: "p.txt")
        try store.store(nodeID: "p", version: 2, from: try tmpFile("v2"), pinned: false, relPath: "p.txt")
        XCTAssertEqual(try store.status(nodeID: "p", serverVersion: 2), .pinned)
        try store.evictCached()
        XCTAssertEqual(try String(contentsOf: try XCTUnwrap(store.localURL(nodeID: "p")), encoding: .utf8), "v2")
        try store.unpin(nodeID: "p")
        XCTAssertEqual(try store.status(nodeID: "p", serverVersion: 2), .cached, "an explicit unpin still unpins")
    }

    func testACopyRemovedBehindOurBackReadsAsNotDownloaded() throws {
        let (store, dir) = try makeStore()
        try store.store(nodeID: "n", version: 1, from: try tmpFile("x"), pinned: true, relPath: "docs/n.txt")
        try FileManager.default.removeItem(at: dir.appendingPathComponent("docs/n.txt"))   // Finder did this
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .none, "so the next open downloads again")
        try store.store(nodeID: "n", version: 1, from: try tmpFile("x"), pinned: false, relPath: "docs/n.txt")
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .pinned, "and the pin is still the user's")
    }

    func testOnlyFilesNoRowClaimsAreOfferedForImport() throws {
        let (store, dir) = try makeStore()
        try store.store(nodeID: "c", version: 1, from: try tmpFile("cached"), pinned: false, relPath: "/docs/cached.txt")
        try FileManager.default.createDirectory(at: dir.appendingPathComponent("docs/new"), withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: dir.appendingPathComponent("docs/new/mine.txt"))
        try Data().write(to: dir.appendingPathComponent("docs/.DS_Store"))
        try Data().write(to: dir.appendingPathComponent("docs/._mine.txt"))
        let found = try store.unregisteredFiles()
        // The registered copy is not offered whatever the server index says about it now —
        // deleted or renamed there, it is still a copy and never goes back up.
        XCTAssertEqual(found.map(\.relPath), ["docs/new/mine.txt"], "index spelling: no leading slash")
    }

    func testARenameOnTheServerDoesNotLeaveAnOrphanToImport() throws {
        let (store, dir) = try makeStore()
        try store.store(nodeID: "n", version: 1, from: try tmpFile("v1"), pinned: false, relPath: "old/name.txt")
        XCTAssertEqual(try store.unregisteredFiles(), [], "not yet downloaded again: the old copy is still claimed")
        try store.store(nodeID: "n", version: 2, from: try tmpFile("v2"), pinned: false, relPath: "new/name.txt")
        XCTAssertFalse(FileManager.default.fileExists(atPath: dir.appendingPathComponent("old").path))
        XCTAssertEqual(try store.unregisteredFiles(), [])
        XCTAssertEqual(store.localURL(nodeID: "n"), dir.appendingPathComponent("new/name.txt"))
    }

    func testAdoptingAnImportedFileRegistersItWhereItLies() throws {
        let (store, dir) = try makeStore()
        let f = dir.appendingPathComponent("a/mine.txt")
        try FileManager.default.createDirectory(at: f.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: f)
        try store.adopt(fileAt: f, nodeID: "n", version: 1, relPath: "a/mine.txt")
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .cached)
        XCTAssertEqual(try store.unregisteredFiles(), [])
    }

    func testAnImportTheServerFiledAsAConflictCopyFollowsThatName() throws {
        // Someone else took the name first: the server kept theirs and filed ours beside it.
        // The local file is a copy of OUR node, so it takes that node's name — left where
        // it was it would be offered for import again on every activation.
        let (store, dir) = try makeStore()
        let f = dir.appendingPathComponent("a/mine.txt")
        try FileManager.default.createDirectory(at: f.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: f)
        try store.adopt(fileAt: f, nodeID: "conflict", version: 1, relPath: "a/mine (conflict, desktop, 2026-09-21).txt")
        XCTAssertFalse(FileManager.default.fileExists(atPath: f.path))
        let url = try XCTUnwrap(store.localURL(nodeID: "conflict"))
        XCTAssertEqual(url.lastPathComponent, "mine (conflict, desktop, 2026-09-21).txt")
        XCTAssertEqual(try String(contentsOf: url, encoding: .utf8), "mine")
        XCTAssertEqual(try store.unregisteredFiles(), [])
    }

    func testRowsFromBeforeRelPathsAreStillRegistered() throws {
        // A database written by the previous build knows absolute paths only.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir.appendingPathComponent("docs"), withIntermediateDirectories: true)
        try Data("old".utf8).write(to: dir.appendingPathComponent("docs/old.txt"))
        let q = try DatabaseQueue()
        try q.write { db in
            try db.execute(sql: "CREATE TABLE local(node_id TEXT PRIMARY KEY, state TEXT NOT NULL, version INTEGER NOT NULL, path TEXT NOT NULL)")
            try db.execute(sql: "INSERT INTO local VALUES('n','pinned',3,?)", arguments: [dir.appendingPathComponent("docs/old.txt").path])
        }
        let store = try LocalStore(dbQueue: q, contentDir: dir)
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 3), .pinned)
        XCTAssertEqual(try store.unregisteredFiles(), [])
    }
    // MARK: - Second review

    func testARenameThatOnlyChangesCaseKeepsTheFreshCopy() throws {
        // file.txt → FILE.txt: two strings, one file on a case-insensitive volume. Clearing
        // "the copy under the old name" used to delete what had just been written.
        let (store, _) = try makeStore()
        try store.store(nodeID: "n", version: 1, from: try tmpFile("v1"), pinned: false, relPath: "docs/file.txt")
        try store.store(nodeID: "n", version: 2, from: try tmpFile("v2"), pinned: false, relPath: "docs/FILE.txt")
        let url = try XCTUnwrap(store.localURL(nodeID: "n"), "the fresh copy is still there")
        XCTAssertEqual(try String(contentsOf: url, encoding: .utf8), "v2")
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 2), .cached)
        XCTAssertEqual(try store.unregisteredFiles(), [])
    }

    func testAnUploadIsClaimedWithWhatTheServerSaidItBecame() throws {
        // Registered straight from the upload's outcome, index or no index: a failed
        // refresh afterwards must not leave the file to be imported a second time.
        let (store, dir) = try makeStore()
        let f = dir.appendingPathComponent("mine.txt")
        try Data("mine".utf8).write(to: f)
        try store.adopt(fileAt: f, nodeID: "n", version: 7, relPath: "mine.txt")
        XCTAssertEqual(try store.unregisteredFiles(), [])
        XCTAssertEqual(try store.version(nodeID: "n"), 7)
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 8), .stale, "a later edit elsewhere is not credited to these bytes")
    }

    // A database from before relative paths, whose container moved before this build ran.
    func legacyStore(recorded: [(id: String, path: String)], contentDir: URL) throws -> LocalStore {
        let q = try DatabaseQueue()
        try q.write { db in
            try db.execute(sql: "CREATE TABLE local(node_id TEXT PRIMARY KEY, state TEXT NOT NULL, version INTEGER NOT NULL, path TEXT NOT NULL)")
            for r in recorded { try db.execute(sql: "INSERT INTO local VALUES(?,'cached',1,?)", arguments: [r.id, r.path]) }
        }
        return try LocalStore(dbQueue: q, contentDir: contentDir)
    }

    func testCopiesAreFoundAgainAfterTheContainerMoved() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathComponent("content")
        try FileManager.default.createDirectory(at: dir.appendingPathComponent("docs"), withIntermediateDirectories: true)
        try Data("old".utf8).write(to: dir.appendingPathComponent("docs/old.txt"))
        let store = try legacyStore(recorded: [("n", "/gone/Containers/OLD-UUID/Data/content/docs/old.txt")], contentDir: dir)
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .cached)
        XCTAssertEqual(try store.unregisteredFiles(), [], "a moved cache is not the user's import")
    }

    func testAFileARowMayStillMeanIsNotOfferedForImport() throws {
        // The place cannot be recovered (the content folder was called something else):
        // what the row may have meant stays out of the import; the rest does not.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathComponent("content")
        try FileManager.default.createDirectory(at: dir.appendingPathComponent("docs"), withIntermediateDirectories: true)
        try Data("old".utf8).write(to: dir.appendingPathComponent("docs/old.txt"))
        try Data("mine".utf8).write(to: dir.appendingPathComponent("docs/mine.txt"))
        let store = try legacyStore(recorded: [("n", "/gone/elsewhere/docs/old.txt")], contentDir: dir)
        XCTAssertEqual(try store.unregisteredFiles().map(\.relPath), ["docs/mine.txt"])
    }
    // MARK: - Third review

    func testAnAmbiguousOldRootRecoversNothing() throws {
        // /gone/content/docs/content/mine.txt: the old root was /gone/content, the copy is
        // gone, and the user has made an unrelated content/mine.txt. Reading the second
        // "content" as the root would hand that file to the old row — and to eviction.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathComponent("content")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: dir.appendingPathComponent("mine.txt"))
        let store = try legacyStore(recorded: [("n", "/gone/content/docs/content/mine.txt")], contentDir: dir)
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .none, "the row is not placed on the user's file")
        XCTAssertEqual(try store.unregisteredFiles(), [], "nor is the file imported while the row may mean it")
        try store.evictCached()   // drops the row; the file is not its to delete
        XCTAssertEqual(try String(contentsOf: dir.appendingPathComponent("mine.txt"), encoding: .utf8), "mine")
    }

    func testOtherRowsSettleWhichOldRootItWas() throws {
        // The same path next to one that can only have been under /gone/content.
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).appendingPathComponent("content")
        try FileManager.default.createDirectory(at: dir.appendingPathComponent("docs/content"), withIntermediateDirectories: true)
        try Data("copy".utf8).write(to: dir.appendingPathComponent("docs/content/mine.txt"))
        try Data("other".utf8).write(to: dir.appendingPathComponent("a.txt"))
        try Data("user".utf8).write(to: dir.appendingPathComponent("mine.txt"))
        let store = try legacyStore(recorded: [("n", "/gone/content/docs/content/mine.txt"), ("a", "/gone/content/a.txt")], contentDir: dir)
        XCTAssertEqual(store.localURL(nodeID: "n"), dir.appendingPathComponent("docs/content/mine.txt"))
        XCTAssertEqual(try store.status(nodeID: "a", serverVersion: 1), .cached)
        XCTAssertEqual(try store.unregisteredFiles().map(\.relPath), ["mine.txt"], "the user's own file is theirs")
    }

    func testACandidateThatBecameACopyMeanwhileIsNoLongerOne() throws {
        let (store, dir) = try makeStore()
        try Data("mine".utf8).write(to: dir.appendingPathComponent("b.txt"))
        let candidate = try XCTUnwrap(try store.unregisteredFiles().first)
        XCTAssertTrue(try store.isStillUnregistered(candidate))
        // While another file was uploading, the server's b.txt was downloaded over it.
        try store.store(nodeID: "b", version: 3, from: try tmpFile("server"), pinned: false, relPath: "b.txt")
        XCTAssertFalse(try store.isStillUnregistered(candidate))
    }

    func testAConflictCopyStaysOwedItsServerNameUntilItGetsIt() throws {
        let (store, dir) = try makeStore()
        let f = dir.appendingPathComponent("a/mine.txt")
        try FileManager.default.createDirectory(at: f.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: f)
        try store.adopt(fileAt: f, nodeID: "c", version: 1, relPath: "a/mine.txt", awaitingServerName: true)
        // The refresh after the upload failed; the next import still knows.
        XCTAssertEqual(try store.copiesAwaitingServerName(), ["c"])
        XCTAssertEqual(try store.unregisteredFiles(), [])
        try store.settleName(nodeID: "c", serverPath: "a/mine (conflict).txt")
        XCTAssertEqual(store.localURL(nodeID: "c")?.lastPathComponent, "mine (conflict).txt")
        XCTAssertFalse(FileManager.default.fileExists(atPath: f.path))
        XCTAssertEqual(try store.copiesAwaitingServerName(), [])
        // A node that is gone from an up-to-date index: nothing to rename to, nothing owed.
        try Data("x".utf8).write(to: f)
        try store.adopt(fileAt: f, nodeID: "d", version: 1, relPath: "a/mine.txt", awaitingServerName: true)
        try store.settleName(nodeID: "d", serverPath: nil)
        XCTAssertEqual(try store.copiesAwaitingServerName(), [])
        XCTAssertEqual(store.localURL(nodeID: "d"), f)
    }
    // MARK: - Fourth review

    func testAFileReplacedByADownloadDuringItsUploadKeepsThatRegistration() throws {
        // The import sends X from b.txt; meanwhile the server's b.txt (Y) is downloaded over
        // it. The server files X as conflict copy C. What lies at the path is Y: it stays
        // B's copy, and C is not credited with bytes that are not its.
        let (store, dir) = try makeStore()
        try Data("X".utf8).write(to: dir.appendingPathComponent("b.txt"))
        let candidate = try XCTUnwrap(try store.unregisteredFiles().first)
        let stamp = try XCTUnwrap(store.stamp(of: candidate.url))
        try store.store(nodeID: "B", version: 3, from: try tmpFile("Y"), pinned: false, relPath: "b.txt")
        XCTAssertFalse(try store.adoptUploaded(candidate, uploadedAs: stamp, nodeID: "C", version: 1, awaitingServerName: true))
        XCTAssertEqual(try store.status(nodeID: "B", serverVersion: 3), .cached)
        XCTAssertEqual(try String(contentsOf: try XCTUnwrap(store.localURL(nodeID: "B")), encoding: .utf8), "Y")
        XCTAssertEqual(try store.status(nodeID: "C", serverVersion: 1), .none, "C comes from the server when asked for")
        XCTAssertEqual(try store.copiesAwaitingServerName(), [])
    }

    func testAFileRewrittenDuringItsUploadIsNotCreditedWithTheUploadedVersion() throws {
        let (store, dir) = try makeStore()
        let f = dir.appendingPathComponent("a.txt")
        try Data("X".utf8).write(to: f)
        let candidate = try XCTUnwrap(try store.unregisteredFiles().first)
        let stamp = try XCTUnwrap(store.stamp(of: candidate.url))
        try Data("rewritten, longer".utf8).write(to: f)   // an atomic save: another file at the same path
        XCTAssertFalse(try store.adoptUploaded(candidate, uploadedAs: stamp, nodeID: "n", version: 1))
        XCTAssertEqual(try store.status(nodeID: "n", serverVersion: 1), .none)
    }

    func testAnUntouchedFileIsAdoptedAsWhatItWasUploadedAs() throws {
        let (store, dir) = try makeStore()
        try Data("X".utf8).write(to: dir.appendingPathComponent("a.txt"))
        let candidate = try XCTUnwrap(try store.unregisteredFiles().first)
        let stamp = try XCTUnwrap(store.stamp(of: candidate.url))
        XCTAssertTrue(try store.adoptUploaded(candidate, uploadedAs: stamp, nodeID: "n", version: 4))
        XCTAssertEqual(try store.version(nodeID: "n"), 4)
        XCTAssertEqual(try store.unregisteredFiles(), [])
    }
}
