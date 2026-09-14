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

    func testVersionsFollowContentHashAndMetadata() {
        // The system re-downloads when contentVersion changes and re-reads metadata when
        // metadataVersion changes; a rename must bump the latter but not the former.
        let a = ProviderItemInfo(node: node("f", parent: nil, name: "x", version: 3, hash: "abc"))
        let renamed = ProviderItemInfo(node: node("f", parent: nil, name: "y", version: 4, hash: "abc"))
        let rewritten = ProviderItemInfo(node: node("f", parent: nil, name: "x", version: 5, hash: "def"))
        XCTAssertEqual(a.contentVersion, renamed.contentVersion)
        XCTAssertNotEqual(a.metadataVersion, renamed.metadataVersion)
        XCTAssertNotEqual(a.contentVersion, rewritten.contentVersion)
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
