import XCTest
@testable import DiscoKit

final class PhotoTransferStoreTests: XCTestCase {
    func testRestartRetainsCheckpointAndExactChunkBytes() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let exported = root.appendingPathComponent("export")
        var store: PhotoTransferStore? = try PhotoTransferStore(directory: root)
        let bytes = Data((0..<17).map(UInt8.init))
        try bytes.write(to: exported)
        var job = PhotoTransfer(account: "a", assetID: "photo", modified: Date(timeIntervalSince1970: 12),
                                created: Date(), parentID: "folder", name: "photo.dng", sha: "hash", bytes: 17)
        try store!.stage(job, exportedFile: exported)
        job.uploadID = "server-session"; job.nextChunk = 2
        try store!.save(job)
        store = nil
        let reopened = try PhotoTransferStore(directory: root)
        let restored = try XCTUnwrap(reopened.all(account: "a").first)
        XCTAssertEqual(restored, job)
        XCTAssertEqual(try Data(contentsOf: reopened.writeChunk(restored, chunkSize: 6)), bytes.suffix(5))
        XCTAssertEqual(try Data(contentsOf: reopened.assetURL(restored)), bytes)
        XCTAssertTrue(try reopened.all(account: "other").isEmpty)
    }

    func testDuplicateDoesNotLoseOriginalAndLogoutIsAccountScoped() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = try PhotoTransferStore(directory: root)
        func stage(_ account: String) throws -> PhotoTransfer {
            let file = root.appendingPathComponent(UUID().uuidString)
            try Data([1,2,3]).write(to: file)
            let job = PhotoTransfer(account: account, assetID: "same", modified: Date(timeIntervalSince1970: 2), created: Date(), parentID: "p", name: "a.dng", sha: "s", bytes: 3)
            try store.stage(job, exportedFile: file)
            return job
        }
        let first = try stage("a")
        XCTAssertThrowsError(try stage("a"))
        XCTAssertTrue(FileManager.default.fileExists(atPath: store.assetURL(first).path))
        let second = try stage("b")
        try store.wipe(account: "a")
        XCTAssertTrue(try store.all(account: "a").isEmpty)
        XCTAssertEqual(try store.all(account: "b").count, 1)
        XCTAssertTrue(FileManager.default.fileExists(atPath: store.assetURL(second).path))
    }

    func testCorruptOrOutOfRangeChunkCannotBePublished() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = try PhotoTransferStore(directory: root)
        let file = root.appendingPathComponent("export")
        try Data([1,2]).write(to: file)
        var job = PhotoTransfer(account: "a", assetID: "p", modified: Date(), created: Date(), parentID: "p", name: "a", sha: "s", bytes: 3)
        try store.stage(job, exportedFile: file)
        XCTAssertThrowsError(try store.writeChunk(job, chunkSize: 3))
        job.nextChunk = Int.max
        XCTAssertThrowsError(try store.writeChunk(job))
        job.nextChunk = -1
        XCTAssertThrowsError(try store.writeChunk(job))
    }

    func testRetryNeverBecomesPermanentAndHasBoundedBackoff() {
        var job = PhotoTransfer(account: "a", assetID: "p", modified: Date(), created: Date(), parentID: "p", name: "a", sha: "s", bytes: 3)
        let now = Date(timeIntervalSince1970: 10)
        job.deferRetry("offline", now: now)
        XCTAssertEqual(job.retryAt.timeIntervalSince(now), 30)
        for _ in 0..<20 { job.deferRetry("offline", now: now) }
        XCTAssertEqual(job.retryAt.timeIntervalSince(now), 3600)
    }
    func testLegacyQueueRetainsEightMiBOffsets() throws {
        let job = PhotoTransfer(account: "a", assetID: "p", modified: Date(), created: Date(), parentID: "p", name: "a", sha: "s", bytes: 3)
        XCTAssertEqual(job.transferChunkSize, 64 << 20)
        var json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(job)) as? [String: Any])
        json.removeValue(forKey: "chunkSize")
        let restored = try JSONDecoder().decode(PhotoTransfer.self, from: JSONSerialization.data(withJSONObject: json))
        XCTAssertEqual(restored.transferChunkSize, 8 << 20)
    }

    func testSweepDropsOtherAccountsAndOrphanFoldersButKeepsTheCurrentQueue() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = try PhotoTransferStore(directory: root)
        func stage(_ account: String, asset: String) throws -> PhotoTransfer {
            let file = root.appendingPathComponent(UUID().uuidString)
            try Data(repeating: 7, count: 1000).write(to: file)
            let job = PhotoTransfer(account: account, assetID: asset, modified: Date(timeIntervalSince1970: 1),
                                    created: Date(), parentID: "p", name: "a.dng", sha: "s", bytes: 1000)
            try store.stage(job, exportedFile: file)
            return job
        }
        let mine = try stage("current", asset: "1")
        let former = try stage("former-pairing", asset: "2")
        // A copy left by a crash between moving the export and recording it.
        let orphan = root.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: orphan, withIntermediateDirectories: true)
        try Data(repeating: 1, count: 500).write(to: orphan.appendingPathComponent("asset"))

        let freed = try store.sweep(keepingAccount: "current")

        XCTAssertEqual(freed, 1500)
        XCTAssertEqual(try store.all(account: "current"), [mine])
        XCTAssertTrue(FileManager.default.fileExists(atPath: store.assetURL(mine).path))
        XCTAssertTrue(try store.all(account: "former-pairing").isEmpty)
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.assetURL(former).path))
        XCTAssertFalse(FileManager.default.fileExists(atPath: orphan.path))
        // The database itself is not a stray.
        XCTAssertNoThrow(try PhotoTransferStore(directory: root).all(account: "current"))
        XCTAssertEqual(try store.sweep(keepingAccount: "current"), 0)
    }
}
