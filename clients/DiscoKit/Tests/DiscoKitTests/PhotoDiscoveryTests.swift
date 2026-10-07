import XCTest
import GRDB
@testable import DiscoKit

final class PhotoDiscoveryTests: XCTestCase {
    private func stamp(_ id: String, _ time: Double = 100) -> PhotoAssetStamp {
        PhotoAssetStamp(id: id, modified: Date(timeIntervalSince1970: time))
    }
    private final class Source: PhotoDiscoverySource {
        var snapshots = 0
        var full = PhotoLibraryDelta(updated: [], token: Data([1]))
        var pages: [PhotoLibraryDelta] = []
        var error: Error?
        func snapshot() throws -> PhotoLibraryDelta { snapshots += 1; return full }
        func changes(since: Data, consume: (PhotoLibraryDelta) throws -> Void) throws {
            for page in pages { try consume(page) }
            if let error { throw error }
        }
    }
    func testTenYearLibraryIsNotRescannedOnNextPass() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        let source = Source()
        source.full = PhotoLibraryDelta(updated: (0..<20_000).map { stamp("old-\($0)") }, token: Data([1]))
        try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: true)
        XCTAssertEqual(try journal.counts().skipped, 20_000)
        XCTAssertTrue(try journal.pendingPhotos(limit: 20).isEmpty)
        source.pages = [PhotoLibraryDelta(updated: [stamp("new")], token: Data([2]))]
        try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: false)
        XCTAssertEqual(source.snapshots, 1)
        XCTAssertEqual(try journal.pendingPhotos(limit: 20).map(\.id), ["new"])
        XCTAssertEqual(try journal.photoDiscoveryCursor(), Data([2]))
    }
    func testPendingSurvivesReopenAndOldUploadCannotEraseEdit() throws {
        let path = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).path
        defer { try? FileManager.default.removeItem(atPath: path) }
        do {
            let journal = try UploadJournal(path: path)
            try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("photo", 200)], token: Data([3])))
        }
        let journal = try UploadJournal(path: path)
        try journal.markSent(assetID: "photo", modified: stamp("photo").modified, bytes: 10, sha: nil, serverName: "old.jpg")
        XCTAssertEqual(try journal.pendingPhotos(limit: 20), [stamp("photo", 200)])
        try journal.markSent(assetID: "photo", modified: stamp("photo", 200).modified, bytes: 10, sha: nil, serverName: "new.jpg")
        XCTAssertTrue(try journal.pendingPhotos(limit: 20).isEmpty)
    }
    func testCursorDoesNotAdvanceWhenDatabaseWriteFails() throws {
        let db = try DatabaseQueue(); let journal = try UploadJournal(dbQueue: db)
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [], token: Data([1])))
        try db.write { try $0.execute(sql: "CREATE TRIGGER fail_pending BEFORE INSERT ON photo_discovery_pending BEGIN SELECT RAISE(ABORT, 'disk failure'); END") }
        XCTAssertThrowsError(try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("new")], token: Data([2]))))
        XCTAssertEqual(try journal.photoDiscoveryCursor(), Data([1]))
    }
    func testInterruptedHistoryResumesFromCommittedPage() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [], token: Data([1])))
        let source = Source()
        source.pages = [PhotoLibraryDelta(updated: [stamp("new")], token: Data([2]))]
        source.error = CancellationError()
        XCTAssertThrowsError(try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: false))
        XCTAssertEqual(try journal.photoDiscoveryCursor(), Data([2]))
        XCTAssertEqual(try journal.pendingPhotos(limit: 20).map(\.id), ["new"])
        XCTAssertEqual(source.snapshots, 0)
    }
    func testExpiredCursorDoesNotReuploadSentOrReseedMissedPhotos() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        try journal.markSent(assetID: "sent", modified: stamp("sent").modified, bytes: 1, sha: nil, serverName: "sent.jpg")
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("deleted")], token: Data([1])))
        let source = Source(); source.error = PhotoDiscoveryError.invalidCursor
        source.full = PhotoLibraryDelta(updated: [stamp("sent"), stamp("missed")], token: Data([4]))
        try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: false)
        XCTAssertEqual(try journal.pendingPhotos(limit: 20).map(\.id), ["missed"])
        XCTAssertEqual(try journal.photoDiscoveryCursor(), Data([4]))
    }
    func testArchiveAndLogoutMaintainDiscoveryState() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("old")], token: Data([1])), seedPreexisting: true)
        XCTAssertEqual(try journal.unseed(), 1)
        XCTAssertEqual(try journal.pendingPhotos(limit: 20).map(\.id), ["old"])
        try journal.wipe()
        XCTAssertNil(try journal.photoDiscoveryCursor())
        XCTAssertTrue(try journal.pendingPhotos(limit: 20).isEmpty)
    }
    func testDeferredAndQueuedAssetsDoNotStarveOtherCandidates() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("retry", 300), stamp("queued", 200), stamp("new", 100)], token: Data([1])))
        try journal.markDeferred(assetID: "retry", modified: stamp("retry", 300).modified, error: "offline")
        XCTAssertEqual(try journal.pendingPhotos(limit: 1, excluding: ["queued"]).map(\.id), ["new"])
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [], deleted: ["new"], token: Data([2])))
        XCTAssertTrue(try journal.pendingPhotos(limit: 1, excluding: ["queued"]).isEmpty)
        XCTAssertEqual(try journal.photoDiscoveryCursor(), Data([2]))
    }

    func testMissingAssetCanBeRemovedAfterMillisecondRoundTrip() throws {
        let journal = try UploadJournal(dbQueue: DatabaseQueue())
        try journal.applyPhotoDiscovery(PhotoLibraryDelta(updated: [stamp("missing", 1790911472.296)], token: Data([1])))
        let pending = try XCTUnwrap(journal.pendingPhotos(limit: 1).first)
        try journal.forgetPendingPhoto(pending)
        XCTAssertTrue(try journal.pendingPhotos(limit: 1).isEmpty)
    }

}
