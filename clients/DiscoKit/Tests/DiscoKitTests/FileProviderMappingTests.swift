import XCTest
import GRDB
@testable import DiscoKit

// The pure part of the File Provider extension: how an index node becomes a provider
// item, and how the sync anchor round-trips. Kept out of the extension so it runs in
// `swift test` without a File Provider domain.
final class FileProviderMappingTests: XCTestCase {
    func node(_ id: String, parent: String?, name: String, dir: Bool = false,
              version: Int64 = 1, hash: String = "h", size: Int64 = 0) -> Node {
        Node(id: id, parentID: parent, name: name, isDir: dir, version: version,
             contentHash: hash, size: size, path: "/" + name)
    }

    func testRootLevelNodeHangsOffTheRootContainer() {
        let info = ProviderItemInfo(node: node("n1", parent: nil, name: "a.txt", size: 12))
        XCTAssertEqual(info.identifier, "n1")
        XCTAssertEqual(info.parentIdentifier, ProviderItemInfo.rootIdentifier)
        XCTAssertEqual(info.filename, "a.txt")
        XCTAssertEqual(info.size, 12)
        XCTAssertFalse(info.isDirectory)
    }

    func testChildKeepsItsParentIdentifier() {
        let info = ProviderItemInfo(node: node("f", parent: "d", name: "b.md"))
        XCTAssertEqual(info.parentIdentifier, "d")
    }

    func testVersionsFollowContentAndMetadata() {
        // The system re-reads metadata when metadataVersion changes and fetches again when
        // contentVersion does. contentVersion names the server version next to the bytes —
        // it is the base an edit is guarded with — so a rename, which bumps the version on
        // the server, changes it too.
        let a = ProviderItemInfo(node: node("f", parent: nil, name: "x", version: 3, hash: "abc"))
        let same = ProviderItemInfo(node: node("f", parent: nil, name: "x", version: 3, hash: "abc"))
        let renamed = ProviderItemInfo(node: node("f", parent: nil, name: "y", version: 4, hash: "abc"))
        let rewritten = ProviderItemInfo(node: node("f", parent: nil, name: "x", version: 5, hash: "def"))
        XCTAssertEqual(a.contentVersion, same.contentVersion)
        XCTAssertNotEqual(a.metadataVersion, renamed.metadataVersion)
        XCTAssertNotEqual(a.contentVersion, renamed.contentVersion)
        XCTAssertNotEqual(a.contentVersion, rewritten.contentVersion)
        XCTAssertEqual(ContentVersionCodec.decode(a.contentVersion), .init(version: 3, hash: "abc"))
    }

    // MARK: - The base version of an edit

    func testContentVersionRoundTripsAndReadsTheOldFormat() {
        XCTAssertEqual(String(decoding: ContentVersionCodec.encode(version: 7, hash: "abc"), as: UTF8.self), "v1:7:abc")
        XCTAssertEqual(ContentVersionCodec.decode(Data("v1:7:abc".utf8)), .init(version: 7, hash: "abc"))
        XCTAssertEqual(ContentVersionCodec.decode(Data("v1:7:".utf8)), .init(version: 7, hash: ""), "a file the server has no hash for")
        // Before this format the value was the bare hash: readable, version unknown.
        XCTAssertEqual(ContentVersionCodec.decode(Data("abc".utf8)), .init(version: nil, hash: "abc"))
        for bad in ["", "v1:", "v1:x:abc", "v1:0:abc", "v1:-2:abc", "v1:7"] {
            XCTAssertNil(ContentVersionCodec.decode(Data(bad.utf8)), bad)
        }
    }

    func testAnEditIsGuardedByTheVersionItWasMadeFrom() {
        // Finder edited v1 while the index already held v2: the base is 1, not 2, so the
        // server files the edit as a conflict copy instead of laying it over v2.
        let current = node("f", parent: nil, name: "x", version: 2, hash: "new")
        let base = ContentVersionCodec.encode(version: 1, hash: "old")
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: base, current: current), 1)
        // Nothing changed meanwhile.
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: ContentVersionCodec.encode(version: 2, hash: "new"), current: current), 2)
    }

    func testAnUnknownBaseIsAConflictNeverTheCurrentVersion() {
        let current = node("f", parent: nil, name: "x", version: 2, hash: "new")
        // An old-format base of other bytes, and one that cannot be read at all.
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: Data("old".utf8), current: current), ContentVersionCodec.unknownBase)
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: Data("v1:x:new".utf8), current: current), ContentVersionCodec.unknownBase)
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: Data(), current: current), ContentVersionCodec.unknownBase)
        XCTAssertEqual(ContentVersionCodec.unknownBase, 0, "server versions start at 1: 0 matches no existing file")
    }

    func testTheSameBytesMayUseTheCurrentVersion() {
        // The edited bytes are exactly what the server holds: nothing unseen to overwrite.
        let current = node("f", parent: nil, name: "y", version: 4, hash: "abc")
        // …a rename bumped the version under an open file,
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: ContentVersionCodec.encode(version: 3, hash: "abc"), current: current), 4)
        // …or the item still carries the old format and the file has not changed since.
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: Data("abc".utf8), current: current), 4)
        // No hash on either side proves nothing.
        let unhashed = node("f", parent: nil, name: "y", version: 4, hash: "")
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: Data("v1:3:".utf8), current: unhashed), 3)
    }

    // MARK: - Downloaded bytes and the version they are reported as

    func testDownloadedBytesAreCreditedToTheVersionTheyMatch() throws {
        let f = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try Data("hello".utf8).write(to: f)
        let hash = try ContentHash.sha256Hex(of: f)
        XCTAssertEqual(hash, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")

        let v1 = node("f", parent: nil, name: "x", version: 1, hash: hash)
        let v2 = node("f", parent: nil, name: "x", version: 2, hash: "other")
        // The index moved to v2 while v1's bytes were coming down: they are still v1's.
        XCTAssertEqual(ContentHash.owner(ofDownloaded: hash, before: v1, after: v2)?.version, 1)
        // The server had moved on before the download started: the bytes are v2's.
        XCTAssertEqual(ContentHash.owner(ofDownloaded: hash, before: v2, after: v1)?.version, 1)
        // Neither: fetch again rather than put a version on bytes that are not its.
        XCTAssertNil(ContentHash.owner(ofDownloaded: "third", before: v1, after: v2))
        // No hash to check against: the older version, where a wrong guess is a conflict copy.
        let unhashed = node("f", parent: nil, name: "x", version: 1, hash: "")
        XCTAssertEqual(ContentHash.owner(ofDownloaded: hash, before: unhashed, after: v2)?.version, 1)
    }

    func testBytesThatMatchNoVersionCarryNone() {
        let n = node("f", parent: nil, name: "x", version: 5, hash: "abc")
        let info = ProviderItemInfo(node: n, unverifiedBytes: "zzz")
        XCTAssertEqual(ContentVersionCodec.decode(info.contentVersion), .init(version: nil, hash: "zzz"))
        XCTAssertEqual(ContentVersionCodec.uploadBase(editedFrom: info.contentVersion, current: n), ContentVersionCodec.unknownBase)
    }

    func testDirectoryHasNoContentVersionToSpeakOf() {
        let d = ProviderItemInfo(node: node("d", parent: nil, name: "docs", dir: true, hash: ""))
        XCTAssertTrue(d.isDirectory)
        XCTAssertFalse(d.contentVersion.isEmpty, "a version must never be empty data")
    }

    func testSyncAnchorRoundTrips() {
        XCTAssertEqual(SyncAnchorCodec.decode(SyncAnchorCodec.encode(0)), 0)
        XCTAssertEqual(SyncAnchorCodec.decode(SyncAnchorCodec.encode(9_876_543_210)), 9_876_543_210)
        XCTAssertNil(SyncAnchorCodec.decode(Data("garbage".utf8)))
    }

    func testCursorOnlyMovesForward() throws {
        // Two writers share the index (the app and the extension); whichever applied the
        // later page wins, an older page must not wind the cursor back.
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.setCursor(10)
        try store.setCursor(7)
        XCTAssertEqual(try store.cursor(), 10)
        try store.setCursor(12)
        XCTAssertEqual(try store.cursor(), 12)
    }

    func testASecondWriterWaitsForTheFirstInsteadOfFailing() throws {
        // The app and the extension each open the same file; one holds a write while the
        // other applies its page. The second must wait, not report "database is locked".
        let path = NSTemporaryDirectory() + "index-\(UUID().uuidString).sqlite"
        defer { try? FileManager.default.removeItem(atPath: path) }
        let a = try IndexStore(path: path), b = try IndexStore(path: path)
        let held = DispatchSemaphore(value: 0)
        let done = expectation(description: "a released")
        DispatchQueue.global().async {
            try? a.holdingWrite { held.signal(); Thread.sleep(forTimeInterval: 0.5) }
            done.fulfill()
        }
        held.wait()
        XCTAssertNoThrow(try b.setCursor(5))
        wait(for: [done], timeout: 5)
        XCTAssertEqual(try b.cursor(), 5)
    }

    func testAllNodesListsEveryRow() throws {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.apply([
            RemoteChange(seq: 1, op: "put", nodeID: "d", path: "/d", isDir: true, version: 1, contentHash: "", size: 0, deleted: false),
            RemoteChange(seq: 2, op: "put", nodeID: "f", path: "/d/f", isDir: false, version: 1, contentHash: "x", size: 1, deleted: false),
        ])
        XCTAssertEqual(Set(try store.allNodes().map(\.id)), ["d", "f"])
    }

    func testFinderHousekeepingNamesStayLocal() {
        for name in [".DS_Store", "Icon\r", "._photo.jpg", ".localized"] {
            XCTAssertTrue(LocalOnlyNames.isLocalOnly(name), name)
        }
        for name in ["Icon", "notes.md", ".hidden-but-mine", "photo.jpg"] {
            XCTAssertFalse(LocalOnlyNames.isLocalOnly(name), name)
        }
    }
}
