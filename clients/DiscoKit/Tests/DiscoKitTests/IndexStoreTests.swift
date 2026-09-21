import XCTest
import GRDB
@testable import DiscoKit

final class IndexStoreTests: XCTestCase {
    func makeStore() throws -> IndexStore { try IndexStore(dbQueue: DatabaseQueue()) }

    func testApplyPutThenReadNode() throws {
        let store = try makeStore()
        let ch = RemoteChange(seq: 1, op: "put", nodeID: "n1", path: "/docs/a.txt",
                              isDir: false, version: 5, contentHash: "h", size: 10, deleted: false)
        try store.apply([ch])
        let node = try store.node(id: "n1")
        XCTAssertEqual(node?.name, "a.txt")
        XCTAssertEqual(node?.version, 5)
        XCTAssertNil(node?.parentID) // /docs parent was not in the batch → nil
    }

    func testChildrenByParentNodeID() throws {
        let store = try makeStore()
        try store.apply([
            RemoteChange(seq: 1, op: "put", nodeID: "dir", path: "/docs", isDir: true,
                         version: 1, contentHash: "", size: 0, deleted: false),
            RemoteChange(seq: 2, op: "put", nodeID: "f1", path: "/docs/a.txt", isDir: false,
                         version: 1, contentHash: "", size: 3, deleted: false),
            RemoteChange(seq: 3, op: "put", nodeID: "root1", path: "/top.txt", isDir: false,
                         version: 1, contentHash: "", size: 1, deleted: false),
        ])
        XCTAssertEqual(try store.children(of: "dir").map(\.id), ["f1"])
        XCTAssertEqual(Set(try store.children(of: nil).map(\.id)), ["dir", "root1"])
    }

    func testDeleteSubtreeAndCursor() throws {
        let store = try makeStore()
        try store.apply([
            RemoteChange(seq: 1, op: "put", nodeID: "dir", path: "/d", isDir: true, version: 1, contentHash: "", size: 0, deleted: false),
            RemoteChange(seq: 2, op: "put", nodeID: "f1", path: "/d/a", isDir: false, version: 1, contentHash: "", size: 1, deleted: false),
        ])
        try store.setCursor(2)
        XCTAssertEqual(try store.cursor(), 2)
        try store.apply([
            RemoteChange(seq: 3, op: "del", nodeID: "dir", path: "/d", isDir: true, version: 2, contentHash: "", size: 0, deleted: true),
        ])
        XCTAssertNil(try store.node(id: "dir"))
        XCTAssertNil(try store.node(id: "f1"))
    }
    func testDeletingAFolderLeavesLookalikeSiblingsAlone() throws {
        // "_" and "%" are LIKE wildcards: deleting "a_" used to take "ab/file.txt" with it.
        let store = try IndexStore(dbQueue: DatabaseQueue())
        func put(_ seq: Int64, _ id: String, _ path: String, dir: Bool) -> RemoteChange {
            RemoteChange(seq: seq, op: "put", nodeID: id, path: path, isDir: dir, version: 1, contentHash: "", size: 0, deleted: false)
        }
        try store.apply([
            put(1, "d1", "a_", dir: true), put(2, "f1", "a_/own.txt", dir: false),
            put(3, "d2", "ab", dir: true), put(4, "f2", "ab/file.txt", dir: false),
            put(5, "d3", "50%", dir: true), put(6, "f3", "50%/x", dir: false),
            put(7, "d4", "50 off", dir: true), put(8, "f4", "50 off/y", dir: false),
        ])
        try store.apply([
            RemoteChange(seq: 9, op: "del", nodeID: "d1", path: "a_", isDir: true, version: 2, contentHash: "", size: 0, deleted: true),
            RemoteChange(seq: 10, op: "del", nodeID: "d3", path: "50%", isDir: true, version: 2, contentHash: "", size: 0, deleted: true),
        ])
        XCTAssertNil(try store.node(id: "f1"), "the folder's own file goes with it")
        XCTAssertNil(try store.node(id: "f3"))
        XCTAssertNotNil(try store.node(id: "f2"), "ab/file.txt is not under a_")
        XCTAssertNotNil(try store.node(id: "f4"), "\"50 off/y\" is not under \"50%\"")
    }

    func testPathsHaveOneSpellingWhateverTheCallerWrites() throws {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.apply([RemoteChange(seq: 1, op: "put", nodeID: "f", path: "docs/a.txt", isDir: false,
                                      version: 1, contentHash: "", size: 0, deleted: false)])
        XCTAssertEqual(try store.node(atPath: "/docs/a.txt")?.id, "f")
        XCTAssertEqual(try store.node(atPath: "docs/a.txt")?.id, "f")
        XCTAssertEqual(try store.node(atPath: "docs//a.txt/")?.id, "f")
        XCTAssertEqual(IndexStore.normalize("/a/b/"), "a/b")
        XCTAssertEqual(IndexStore.normalize(""), "")
    }
    // MARK: - Two processes, one index

    private func change(_ seq: Int64, _ id: String, _ path: String, version: Int64, deleted: Bool = false) -> RemoteChange {
        RemoteChange(seq: seq, op: deleted ? "del" : "put", nodeID: id, path: path, isDir: false,
                     version: version, contentHash: "h\(version)", size: 0, deleted: deleted)
    }

    func testALatePageDoesNotPutAnOlderVersionBack() throws {
        // The app has applied v2 (seq 101); the extension then applies the page it fetched
        // earlier, which still says v1.
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.apply([change(100, "f", "a.txt", version: 1), change(101, "f", "a.txt", version: 2)])
        try store.apply([change(100, "f", "a.txt", version: 1)])
        XCTAssertEqual(try store.node(id: "f")?.version, 2)
        XCTAssertEqual(try store.cursor(), 101)
    }

    func testALatePageDoesNotBringBackADeletedNodeNorDeleteARestoredOne() throws {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.apply([change(10, "f", "a.txt", version: 1), change(11, "f", "a.txt", version: 2, deleted: true)])
        try store.apply([change(10, "f", "a.txt", version: 1)])
        XCTAssertNil(try store.node(id: "f"), "deleted stays deleted")
        try store.apply([change(12, "f", "a.txt", version: 3)])   // restored from the trash
        try store.apply([change(11, "f", "a.txt", version: 2, deleted: true)])
        XCTAssertEqual(try store.node(id: "f")?.version, 3, "and restored stays restored")
    }

    func testTheCursorMovesWithTheRowsItCovers() throws {
        // In one transaction: between a page's rows and its cursor there is no moment at
        // which the other process could take the rows for unapplied.
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.apply([change(5, "f", "a.txt", version: 1)])
        XCTAssertEqual(try store.cursor(), 5)
        try store.apply([])
        try store.setCursor(3)
        XCTAssertEqual(try store.cursor(), 5, "and never backwards")
    }
}
