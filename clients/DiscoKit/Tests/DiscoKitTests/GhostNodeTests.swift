import XCTest
import GRDB
@testable import DiscoKit

// A ghost is a node the index still lists after the server hard-deleted it and the delete
// event never arrived (the macOS app showed new1/DiscoDrive.app, and deleting it failed with
// "The server rejected the request (404)"). The server answers 404 {"error":"not found"}
// about it; the client then forgets the node and its subtree, and nothing else.
final class GhostNodeTests: XCTestCase {
    private func store() throws -> IndexStore {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        func put(_ seq: Int64, _ id: String, _ path: String, dir: Bool = false) -> RemoteChange {
            RemoteChange(seq: seq, op: "create", nodeID: id, path: path, isDir: dir, version: 1, contentHash: "", size: 0, deleted: false)
        }
        try store.apply([
            put(1, "new1", "new1", dir: true),
            put(2, "app", "new1/DiscoDrive.app", dir: true),
            put(3, "plist", "new1/DiscoDrive.app/Info.plist"),
            put(4, "zip", "new1/DiscoDrive.app.zip"),
            put(5, "keep", "new1/keep.txt"),
        ])
        return store
    }

    // Every request answers with `status` and `body`, as the server would for a missing node.
    private func client(status: Int, body: [String: String]) -> APIClient {
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("/auth/device/token") {
                return (200, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: ["token": "jwt"]))
            }
            return (status, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: body))
        }
        return APIClient(baseURL: URL(string: "https://x.test")!, deviceToken: "D", session: MockURLProtocol.session())
    }

    private func ids(_ store: IndexStore) throws -> Set<String> { Set(try store.allNodes().map(\.id)) }

    func testDeleteOfAGhostSucceedsAndForgetsItsSubtree() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        // What AppState.deleteNode and the File Provider run: a gone node is a done delete.
        try await index.deleteForgettingGone(nodeID: "app") { try await api.delete(nodeID: "app") }
        XCTAssertEqual(try ids(index), ["new1", "zip", "keep"], "the ghost and its subtree go, nothing else")
    }

    func testRenameOfAGhostForgetsItAndReportsNodeNotFound() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        do {
            try await index.forgettingIfGone("app") { try await api.rename(nodeID: "app", newName: "x") }
            XCTFail("a rename of a node the server does not have cannot succeed")
        } catch APIError.nodeNotFound {}
        XCTAssertEqual(try ids(index), ["new1", "zip", "keep"])
    }

    func testDownloadOfAGhostForgetsIt() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        let dst = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        do {
            try await index.forgettingIfGone("plist") { try await api.download(nodeID: "plist", to: dst) }
            XCTFail("want nodeNotFound")
        } catch APIError.nodeNotFound {}
        XCTAssertEqual(try ids(index), ["new1", "app", "zip", "keep"])
        XCTAssertFalse(FileManager.default.fileExists(atPath: dst.path))
    }

    func testVersionsOfAGhostReportNodeNotFound() async throws {
        let api = client(status: 404, body: ["error": "not found"])
        do { _ = try await api.versions(nodeID: "plist"); XCTFail("want nodeNotFound") } catch APIError.nodeNotFound {}
        do { _ = try await api.shares(nodeID: "plist"); XCTFail("want nodeNotFound") } catch APIError.nodeNotFound {}
    }

    // Only the server's own "not found" about the node counts. Other 404s — a blob missing
    // on disk, a proxy's page — and other failures leave the index alone.
    func testOtherFailuresForgetNothing() async throws {
        for (status, body) in [(404, ["error": "file not found"]), (404, ["message": "nginx"]), (500, ["error": "not found"])] {
            let index = try store()
            let api = client(status: status, body: body)
            do {
                try await index.deleteForgettingGone(nodeID: "app") { try await api.delete(nodeID: "app") }
                XCTFail("\(status) \(body): the failure must surface")
            } catch APIError.http(let code) {
                XCTAssertEqual(code, status)
            }
            XCTAssertEqual(try ids(index).count, 5, "\(status) \(body) must not drop anything")
        }
    }

    // A 404 about an upload is never "done": the chunked uploader still sees it as a stale
    // session to re-init, not as a gone node.
    func testUploadSessionNotFoundStaysAPlainStatus() async throws {
        let api = client(status: 404, body: ["error": "upload session not found"])
        do { _ = try await api.uploadStatus(uploadID: "u1"); XCTFail("want a 404") } catch APIError.http(404) {}
    }

    func testForgetOnlyTakesTheSubtree() throws {
        let index = try store()
        XCTAssertEqual(Set(try index.forget(nodeID: "app")), ["app", "plist"])
        XCTAssertEqual(try ids(index), ["new1", "zip", "keep"])
        XCTAssertEqual(try index.forget(nodeID: "unknown"), [])
    }

    func testTheGoneMessageIsLocalized() {
        for lang in L10n.supported {
            XCTAssertFalse(L10n.table["status.nodeGone"]?[lang]?.isEmpty ?? true, "status.nodeGone missing in \(lang)")
        }
    }
}

extension GhostNodeTests {
    // A move names two nodes; the server's "not found" may be about either.
    func testMoveIntoAGhostFolderForgetsTheFolderNotTheFile() async throws {
        let index = try store()
        let gone: Set<String> = ["app", "plist"]
        let forgotten = await index.forgetGoneAfterMove(nodeID: "keep", parentID: "app") { !gone.contains($0) }
        XCTAssertEqual(Set(forgotten), ["app", "plist"])
        XCTAssertEqual(try ids(index), ["new1", "zip", "keep"])
    }

    func testAMoveWhoseQuestionFailsForgetsNothing() async throws {
        let index = try store()
        let forgotten = await index.forgetGoneAfterMove(nodeID: "keep", parentID: "app") { _ in throw URLError(.notConnectedToInternet) }
        XCTAssertEqual(forgotten, [])
        XCTAssertEqual(try ids(index).count, 5)
    }

    func testNodeExistsAsksTheServer() async throws {
        let api = client(status: 404, body: ["error": "not found"])
        let exists = try await api.nodeExists(nodeID: "app")
        XCTAssertFalse(exists)
    }
}

// Fix round 1: case-sensitive subtrees, and feed deletes applied by id to the indexed path.
extension GhostNodeTests {
    private func change(_ seq: Int64, _ id: String, _ path: String, dir: Bool = false, deleted: Bool = false) -> RemoteChange {
        RemoteChange(seq: seq, op: deleted ? "delete" : "create", nodeID: id, path: path, isDir: dir,
                     version: 1, contentHash: "", size: 0, deleted: deleted)
    }

    // The server allows "Docs" and "docs" as siblings; SQLite's LIKE ignores ASCII case.
    func testForgettingAGhostKeepsASiblingThatDiffersOnlyInCase() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "D", "Docs", dir: true), change(2, "Da", "Docs/a"),
                         change(3, "d", "docs", dir: true), change(4, "db", "docs/b")])
        XCTAssertEqual(Set(try index.forget(nodeID: "D")), ["D", "Da"])
        XCTAssertEqual(try ids(index), ["d", "db"])
    }

    func testAFeedDeleteKeepsACaseSibling() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "D", "Docs", dir: true), change(2, "Da", "Docs/a"),
                         change(3, "d", "docs", dir: true), change(4, "db", "docs/b")])
        try index.apply([change(5, "D", "Docs", dir: true, deleted: true)])
        XCTAssertEqual(try ids(index), ["d", "db"])
    }

    // A purged node is re-announced with its ORIGINAL path, after a new folder took that path.
    // The delete goes by id: an id no longer indexed is a no-op.
    func testAPurgeOfAnOldNodeKeepsTheNewNodeAtItsPath() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "A", "P", dir: true), change(2, "A", "P", dir: true, deleted: true),
                         change(3, "B", "P", dir: true), change(4, "f", "P/f")])
        try index.apply([change(5, "A", "P", dir: true, deleted: true)])
        XCTAssertEqual(try ids(index), ["B", "f"])
    }

    // A delete for a known id removes the subtree where the index has it, whatever path the
    // feed entry names.
    func testAFeedDeleteRemovesTheIndexedSubtreeNotTheFeedPath() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "A", "Q", dir: true), change(2, "Aq", "Q/x"),
                         change(3, "B", "P", dir: true), change(4, "f", "P/f")])
        try index.apply([change(5, "A", "P", dir: true, deleted: true)])
        XCTAssertEqual(try ids(index), ["B", "f"])
    }

    // Parents are resolved case-sensitively too: "docs/b" is not a child of "Docs".
    func testChildrenOfACaseSiblingStayWithTheirOwnFolder() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "db", "docs/b"), change(2, "d", "docs", dir: true), change(3, "D", "Docs", dir: true)])
        XCTAssertEqual(try index.children(of: "D").map(\.id), [])
        XCTAssertEqual(try index.children(of: "d").map(\.id), ["db"])
    }
}

// Fix round 2: paths are not unique. A ghost A can still be indexed at P after a live B was
// created there; removing "the subtree at A's path" took B's tree along.
extension GhostNodeTests {
    private func sharedPathStore() throws -> IndexStore {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "A", "P", dir: true), change(2, "Ax", "P/x"),
                         change(3, "B", "P", dir: true), change(4, "By", "P/y")])
        return index
    }

    func testForgettingAGhostKeepsALiveNodeAtTheSamePath() throws {
        let index = try sharedPathStore()
        XCTAssertEqual(try index.forget(nodeID: "A"), ["A"])
        XCTAssertTrue(try ids(index).isSuperset(of: ["B", "By"]))
        XCTAssertFalse(try ids(index).contains("A"))
    }

    func testAFeedDeleteOfAGhostKeepsALiveNodeAtTheSamePath() throws {
        let index = try sharedPathStore()
        try index.apply([change(5, "A", "P", dir: true, deleted: true)])
        XCTAssertTrue(try ids(index).isSuperset(of: ["B", "By"]))
        XCTAssertFalse(try ids(index).contains("A"))
    }

    // With the path unique, the node's subtree is walked by parent ids.
    func testForgettingAFolderAtAUniquePathTakesItsSubtree() throws {
        let index = try IndexStore(dbQueue: DatabaseQueue())
        try index.apply([change(1, "Q", "Q", dir: true), change(2, "Qs", "Q/s", dir: true),
                         change(3, "Qsz", "Q/s/z"), change(4, "R", "R")])
        XCTAssertEqual(Set(try index.forget(nodeID: "Q")), ["Q", "Qs", "Qsz"])
        XCTAssertEqual(try ids(index), ["R"])
    }
}

// Sharing to an unknown recipient and restoring a missing version answer the same 404 as a
// missing node. With `confirm`, only a node the server says is gone is forgotten.
extension GhostNodeTests {
    func testShareToAnUnknownRecipientKeepsTheNode() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        do {
            _ = try await index.forgettingIfGone("keep", confirm: { _ in true }) {
                try await api.share(nodeID: "keep", email: "nobody@x.test", expiresInSeconds: nil)
            }
            XCTFail("want a 404")
        } catch APIError.http(404) {}
        XCTAssertEqual(try ids(index).count, 5)
    }

    func testRestoreWhoseQuestionFailsKeepsTheNode() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        do {
            try await index.forgettingIfGone("plist", confirm: { _ in throw URLError(.notConnectedToInternet) }) {
                try await api.restoreVersion(nodeID: "plist", version: 9)
            }
            XCTFail("want a 404")
        } catch APIError.http(404) {}
        XCTAssertEqual(try ids(index).count, 5)
    }

    func testConfirmedGoneIsForgotten() async throws {
        let index = try store()
        let api = client(status: 404, body: ["error": "not found"])
        do {
            try await index.forgettingIfGone("app", confirm: { try await api.nodeExists(nodeID: $0) }) {
                try await api.restoreVersion(nodeID: "app", version: 1)
            }
            XCTFail("want nodeNotFound")
        } catch APIError.nodeNotFound {}
        XCTAssertEqual(try ids(index), ["new1", "zip", "keep"])
    }
}

// Round 2: a ghost folder and the live folder that took its path share one path; a child
// created there belongs to the one the feed placed last, whatever the row order.
extension GhostNodeTests {
    func testChildAtASharedPathGoesToTheNewerFolder() throws {
        func ch(_ seq: Int64, _ id: String, _ path: String, dir: Bool) -> RemoteChange {
            RemoteChange(seq: seq, op: "create", nodeID: id, path: path, isDir: dir, version: 1, contentHash: "", size: 0, deleted: false)
        }
        // Either row order: the ghost's row inserted first, or the live folder's.
        for (ghost, live) in [("a", "b"), ("b", "a")] {
            let index = try IndexStore(dbQueue: DatabaseQueue())
            try index.apply([ch(1, ghost, "P", dir: true), ch(2, live, "Q", dir: true)])
            try index.apply([ch(3, live, "P", dir: true)])  // the ghost's delete has not arrived
            try index.apply([ch(4, "c", "P/x.md", dir: false)])
            XCTAssertEqual(try index.node(id: "c")?.parentID, live, "live \(live)")
        }
    }
}

// Round 3: an index written before the seq column existed opens, gains the column (0 for old
// rows), and parent resolution works on it.
extension GhostNodeTests {
    func testOldIndexWithoutSeqMigrates() throws {
        let path = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".sqlite").path
        defer { try? FileManager.default.removeItem(atPath: path) }
        let old = try DatabaseQueue(path: path)
        try old.write { db in
            try db.execute(sql: """
                CREATE TABLE nodes(
                  id TEXT PRIMARY KEY, parent_id TEXT, name TEXT NOT NULL,
                  is_dir INTEGER NOT NULL, version INTEGER NOT NULL,
                  content_hash TEXT NOT NULL, size INTEGER NOT NULL,
                  path TEXT NOT NULL DEFAULT ''
                );
                INSERT INTO nodes(id,parent_id,name,is_dir,version,content_hash,size,path)
                  VALUES('d',NULL,'Docs',1,1,'',0,'Docs');
                CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
                INSERT INTO meta(key,value) VALUES('cursor','5');
            """)
        }
        try old.close()

        let index = try IndexStore(path: path)
        let seq = try index.dbQueue.read { db in try Int64.fetchOne(db, sql: "SELECT seq FROM nodes WHERE id = 'd'") }
        XCTAssertEqual(seq, 0)
        try index.apply([RemoteChange(seq: 6, op: "create", nodeID: "f", path: "Docs/a.md", isDir: false,
                                      version: 1, contentHash: "", size: 0, deleted: false)])
        XCTAssertEqual(try index.node(id: "f")?.parentID, "d")
        XCTAssertEqual(try index.node(id: "d")?.name, "Docs")
    }
}
