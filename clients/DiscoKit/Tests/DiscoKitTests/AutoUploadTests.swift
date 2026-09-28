import XCTest
import GRDB
@testable import DiscoKit

final class NameResolverTests: XCTestCase {

    private func fixed(_ pairs: [String: NameState]) -> (String) -> NameState {
        { pairs[$0] ?? .absent }
    }

    func testFreeNameIsUsedAsIs() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed([:])), .upload("IMG_1.jpg"))
    }

    func testIdenticalContentIsSkipped() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(["IMG_1.jpg": .same])), .alreadyThere)
    }

    func testTakenNameGetsASuffix() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(["IMG_1.jpg": .different])),
                       .upload("IMG_1-1.jpg"))
    }

    func testSuffixKeepsCounting() {
        let taken: [String: NameState] = ["IMG_1.jpg": .different, "IMG_1-1.jpg": .different,
                                          "IMG_1-2.jpg": .different]
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(taken)), .upload("IMG_1-3.jpg"))
    }

    /// A suffixed candidate holding the very same bytes means the photo is already there
    /// under that name — a third copy would be pure noise.
    func testSuffixedCandidateWithSameContentIsSkipped() {
        let state: [String: NameState] = ["IMG_1.jpg": .different, "IMG_1-1.jpg": .same]
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(state)), .alreadyThere)
    }

    func testExtensionEdgeCases() {
        XCTAssertEqual(NameResolver.resolve("VIDEO", exists: fixed(["VIDEO": .different])), .upload("VIDEO-1"))
        XCTAssertEqual(NameResolver.resolve(".config", exists: fixed([".config": .different])), .upload(".config-1"))
        XCTAssertEqual(NameResolver.resolve("clip.tar.gz", exists: fixed(["clip.tar.gz": .different])),
                       .upload("clip.tar-1.gz"))
    }

    /// Giving up beats looping — but it is not "already uploaded": the caller defers the
    /// asset for a later pass instead of recording it as sent.
    func testGivesUpAfterTheCapWithoutCallingItUploaded() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: { _ in .different }), .noFreeName)
    }

    /// Every edited photo's full-size resource is called FullSizeRender.*; uploading under
    /// that name ran out of suffixes after 51 photos. The original's name is used instead,
    /// with the extension of the bytes actually sent.
    func testEditedPhotoKeepsItsOriginalName() {
        XCTAssertEqual(NameResolver.uploadName(resource: "FullSizeRender.jpg", original: "IMG_1234.HEIC"), "IMG_1234.jpg")
        XCTAssertEqual(NameResolver.uploadName(resource: "FullSizeRender.mov", original: "IMG_0007.MOV"), "IMG_0007.mov")
        XCTAssertEqual(NameResolver.uploadName(resource: "IMG_1.HEIC", original: nil), "IMG_1.HEIC")
        XCTAssertEqual(NameResolver.uploadName(resource: "FullSizeRender", original: "IMG_2.HEIC"), "IMG_2")
    }
}

final class UploadJournalTests: XCTestCase {

    private func journal() throws -> UploadJournal {
        try UploadJournal(dbQueue: try DatabaseQueue())   // in-memory
    }

    /// Edited photos recorded as sent without being sent (no free FullSizeRender name) go
    /// back into the queue once; real uploads stay recorded.
    func testMigrationRequeuesUnsentRenders() throws {
        let db = try DatabaseQueue()
        let v = Date(timeIntervalSince1970: 1_000_000)
        do {
            let j = try UploadJournal(dbQueue: db)
            try j.markSent(assetID: "lost", modified: v, bytes: 0, sha: "H", serverName: "FullSizeRender.jpg")
            try j.markSent(assetID: "sent", modified: v, bytes: 99, sha: "H2", serverName: "FullSizeRender-3.jpg")
            try j.markSent(assetID: "same", modified: v, bytes: 0, sha: "H3", serverName: "IMG_1.jpg")
        }
        try db.write { try $0.execute(sql: "PRAGMA user_version = 0") }   // a journal from before
        let j = try UploadJournal(dbQueue: db)
        XCTAssertFalse(try j.isKnown(assetID: "lost", modified: v))
        XCTAssertTrue(try j.isKnown(assetID: "sent", modified: v))
        XCTAssertTrue(try j.isKnown(assetID: "same", modified: v))
    }

    func testGivenUpAssetIsRetriedDaily() throws {
        let j = try journal()
        let v = Date(timeIntervalSince1970: 1_000_000)
        for _ in 0..<5 { try j.markDeferred(assetID: "A", modified: v, error: "offline") }
        XCTAssertFalse(try j.mayRetry(assetID: "A", maxAttempts: 5, retryAfter: 86_400))
        XCTAssertTrue(try j.mayRetry(assetID: "A", maxAttempts: 5, retryAfter: 86_400,
                                     now: Date().addingTimeInterval(86_401)))
        XCTAssertTrue(try j.mayRetry(assetID: "new", maxAttempts: 5, retryAfter: 86_400))
    }

    func testUnknownAssetIsNotKnown() throws {
        let j = try journal()
        XCTAssertFalse(try j.isKnown(assetID: "A1", modified: Date()))
    }

    func testSentAssetIsKnownAtThatVersionOnly() throws {
        let j = try journal()
        let v1 = Date(timeIntervalSince1970: 1_000_000)
        try j.markSent(assetID: "A1", modified: v1, bytes: 10, sha: "H1", serverName: "IMG_1.jpg")
        XCTAssertTrue(try j.isKnown(assetID: "A1", modified: v1))

        // The user edited the photo: same asset, new modification date — new work.
        let v2 = Date(timeIntervalSince1970: 2_000_000)
        XCTAssertFalse(try j.isKnown(assetID: "A1", modified: v2),
                       "an edited photo must count as new content")
    }

    /// A failed upload has to come back around; treating it as known would lose the photo
    /// after a single network hiccup.
    func testDeferredAssetIsRetried() throws {
        let j = try journal()
        let when = Date(timeIntervalSince1970: 1_000_000)
        try j.markDeferred(assetID: "A1", modified: when, error: "network")
        XCTAssertFalse(try j.isKnown(assetID: "A1", modified: when))
        XCTAssertEqual(try j.attempts(assetID: "A1"), 1)
        try j.markDeferred(assetID: "A1", modified: when, error: "network")
        XCTAssertEqual(try j.attempts(assetID: "A1"), 2)
    }

    func testSeedingMarksEverythingWithoutUploading() throws {
        let j = try journal()
        let now = Date()
        try j.seedPreexisting([(id: "A1", modified: now), (id: "A2", modified: now)])
        XCTAssertTrue(try j.isKnown(assetID: "A1", modified: now))
        XCTAssertTrue(try j.isKnown(assetID: "A2", modified: now))
        let counts = try j.counts()
        XCTAssertEqual(counts.skipped, 2)
        XCTAssertEqual(counts.sent, 0)
    }

    func testCountsAndLog() throws {
        let j = try journal()
        let now = Date()
        try j.markSent(assetID: "A1", modified: now, bytes: 1, sha: nil, serverName: "a.jpg")
        try j.markDeferred(assetID: "A2", modified: now, error: "boom")
        let counts = try j.counts()
        XCTAssertEqual(counts.sent, 1)
        XCTAssertEqual(counts.deferred, 1)
        let log = try j.recent(limit: 10)
        XCTAssertEqual(log.count, 2)
        XCTAssertTrue(log.contains { $0.error == "boom" })
    }

    /// A retry that finally succeeds must clear the failure, not leave the asset looking
    /// broken in the log forever.
    func testSuccessAfterFailureClearsTheError() throws {
        let j = try journal()
        let now = Date()
        try j.markDeferred(assetID: "A1", modified: now, error: "network")
        try j.markSent(assetID: "A1", modified: now, bytes: 5, sha: "H", serverName: "a.jpg")
        XCTAssertEqual(try j.counts().deferred, 0)
        XCTAssertEqual(try j.counts().sent, 1)
        XCTAssertEqual(try j.attempts(assetID: "A1"), 0)
    }
}

/// Uploading the existing library is a deliberate, separate decision — the journal has to
/// support taking it back without losing what already went up.
final class UnseedTests: XCTestCase {

    func testUnseedFreesPreexistingButKeepsSentAndDeferred() throws {
        let j = try UploadJournal(dbQueue: try DatabaseQueue())
        let now = Date()
        try j.seedPreexisting([(id: "A1", modified: now), (id: "A2", modified: now)])
        try j.markSent(assetID: "B1", modified: now, bytes: 1, sha: "H", serverName: "b.jpg")
        try j.markDeferred(assetID: "C1", modified: now, error: "network")

        let freed = try j.unseed()

        XCTAssertEqual(freed, 2, "both pre-existing photos become work again")
        XCTAssertFalse(try j.isKnown(assetID: "A1", modified: now))
        XCTAssertTrue(try j.isKnown(assetID: "B1", modified: now),
                      "an already-uploaded photo must not be uploaded a second time")
        let counts = try j.counts()
        XCTAssertEqual(counts.skipped, 0)
        XCTAssertEqual(counts.sent, 1)
        XCTAssertEqual(counts.deferred, 1)
    }
}
