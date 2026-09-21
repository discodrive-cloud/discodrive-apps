import XCTest
import GRDB
@testable import DiscoKit

final class ChangeDeltaTests: XCTestCase {
    private func change(_ seq: Int64, _ id: String, deleted: Bool = false) -> RemoteChange {
        RemoteChange(seq: seq, op: deleted ? "delete" : "update", nodeID: id,
                     path: id + ".txt", isDir: false, version: seq, contentHash: "h", size: 1, deleted: deleted)
    }

    func testRestoredNodeIsOnlyUpdatedAcrossPageBoundaries() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        var delta = ChangeDelta(cursor: 0)
        let first = [change(1, "restored"), change(2, "restored", deleted: true)]
        let second = [change(3, "restored"), change(4, "gone"), change(5, "gone", deleted: true)]
        for page in [first, second] { try index.apply(page); delta.record(page) }
        XCTAssertNotNil(try index.node(id: "restored"))
        XCTAssertNil(try index.node(id: "gone"))
        XCTAssertEqual(delta.updated, ["restored"])
        XCTAssertEqual(delta.deleted, ["gone"])
    }

    func testRepeatedOrOlderEventsCannotUndoFinalOperation() {
        var delta = ChangeDelta(cursor: 0)
        delta.record([change(3, "a"), change(4, "b", deleted: true)])
        delta.record([change(1, "a", deleted: true), change(3, "a"), change(2, "b")])
        XCTAssertEqual(delta.updated, ["a"])
        XCTAssertEqual(delta.deleted, ["b"])
    }
}
