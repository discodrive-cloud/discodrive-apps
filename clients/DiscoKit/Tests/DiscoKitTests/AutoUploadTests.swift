import XCTest
import GRDB
@testable import DiscoKit

final class NameResolverTests: XCTestCase {

    private func fixed(_ pairs: [String: NameState]) -> (String) -> NameState {
        { pairs[$0] ?? .absent }
    }

    func testFreeNameIsUsedAsIs() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed([:])), "IMG_1.jpg")
    }

    func testIdenticalContentIsSkipped() {
        XCTAssertNil(NameResolver.resolve("IMG_1.jpg", exists: fixed(["IMG_1.jpg": .same])))
    }

    func testTakenNameGetsASuffix() {
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(["IMG_1.jpg": .different])),
                       "IMG_1-1.jpg")
    }

    func testSuffixKeepsCounting() {
        let taken: [String: NameState] = ["IMG_1.jpg": .different, "IMG_1-1.jpg": .different,
                                          "IMG_1-2.jpg": .different]
        XCTAssertEqual(NameResolver.resolve("IMG_1.jpg", exists: fixed(taken)), "IMG_1-3.jpg")
    }

    /// A suffixed candidate holding the very same bytes means the photo is already there
    /// under that name — a third copy would be pure noise.
    func testSuffixedCandidateWithSameContentIsSkipped() {
        let state: [String: NameState] = ["IMG_1.jpg": .different, "IMG_1-1.jpg": .same]
        XCTAssertNil(NameResolver.resolve("IMG_1.jpg", exists: fixed(state)))
    }

    func testExtensionEdgeCases() {
        XCTAssertEqual(NameResolver.resolve("VIDEO", exists: fixed(["VIDEO": .different])), "VIDEO-1")
        XCTAssertEqual(NameResolver.resolve(".config", exists: fixed([".config": .different])), ".config-1")
        XCTAssertEqual(NameResolver.resolve("clip.tar.gz", exists: fixed(["clip.tar.gz": .different])),
                       "clip.tar-1.gz")
    }

    /// Giving up beats looping: something is wrong with the destination, and the caller
    /// records the asset as deferred instead of spinning.
    func testGivesUpAfterTheCap() {
        XCTAssertNil(NameResolver.resolve("IMG_1.jpg", exists: { _ in .different }))
    }
}

final class UploadJournalTests: XCTestCase {

    private func journal() throws -> UploadJournal {
        try UploadJournal(dbQueue: try DatabaseQueue())   // in-memory
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
