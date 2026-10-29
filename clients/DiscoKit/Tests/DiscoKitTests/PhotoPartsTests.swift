import XCTest
import GRDB
@testable import DiscoKit

/// An asset goes up in parts: the original, the edit, the Live Photo's video.
final class PhotoPartsTests: XCTestCase {

    // MARK: names

    func testEditAndLiveNamesFollowTheOriginal() {
        XCTAssertEqual(PhotoParts.editName(original: "IMG_1234.HEIC", resource: "FullSizeRender.jpg"), "IMG_1234_E.jpg")
        XCTAssertEqual(PhotoParts.editName(original: "IMG_1234-1.HEIC", resource: "FullSizeRender.heic"), "IMG_1234-1_E.heic")
        XCTAssertEqual(PhotoParts.editName(original: "IMG_0042.MOV", resource: "FullSizeRender.mov"), "IMG_0042_E.mov")
        XCTAssertEqual(PhotoParts.editName(original: "scan", resource: "FullSizeRender"), "scan_E")
        XCTAssertEqual(PhotoParts.liveName(original: "IMG_1234.HEIC", resource: "IMG_1234.MOV"), "IMG_1234.MOV")
        XCTAssertEqual(PhotoParts.liveName(original: "IMG_1234-2.HEIC", resource: "IMG_1234.MOV"), "IMG_1234-2.MOV")
        // A former upload of the edit kept the original's base with the edit's extension.
        XCTAssertEqual(PhotoParts.originalName(base: "IMG_1234.JPG", resource: "IMG_1234.HEIC"), "IMG_1234.HEIC")
        XCTAssertEqual(PhotoParts.editName(original: "IMG_1234.JPG", resource: "IMG_1234.JPG"), "IMG_1234_E.JPG")
    }

    func testWhatAFormerVersionSent() {
        // Untouched since the upload: it sent the edit when there was one, else the original.
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: false, modifiedUnchanged: true, originalSHA: nil, legacySHA: "A"), .original)
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: true, modifiedUnchanged: true, originalSHA: nil, legacySHA: "A"), .edit)
        // Changed since (edited after the upload, or the edit reverted): the bytes decide.
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: true, modifiedUnchanged: false, originalSHA: "A", legacySHA: "A"), .original)
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: true, modifiedUnchanged: false, originalSHA: "B", legacySHA: "A"), .edit)
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: false, modifiedUnchanged: false, originalSHA: "A", legacySHA: "A"), .original)
        XCTAssertEqual(PhotoParts.legacyKind(hasEdit: false, modifiedUnchanged: false, originalSHA: "B", legacySHA: "A"), .edit,
                       "an edit reverted after the upload: what went up was the edit")
    }

    /// The original lands beside its edit, whatever stopped it going up with the rename.
    func testOriginalNameFromTheEdit() {
        XCTAssertEqual(PhotoParts.originalName(forEdit: "IMG_1234-1_E.JPG", resource: "IMG_1234.HEIC"), "IMG_1234-1.HEIC")
        XCTAssertEqual(PhotoParts.originalName(forEdit: "IMG_1234_E.jpg", resource: "IMG_1234.HEIC"), "IMG_1234.HEIC")
        XCTAssertNil(PhotoParts.originalName(forEdit: "IMG_1234.JPG", resource: "IMG_1234.HEIC"), "not an edit name")
        XCTAssertNil(PhotoParts.originalName(forEdit: "", resource: "IMG_1234.HEIC"))
    }

    // MARK: journal

    private func journal(_ db: DatabaseQueue? = nil) throws -> UploadJournal {
        try UploadJournal(dbQueue: db ?? (try DatabaseQueue()))
    }

    func testPartsAreRecordedAndForgotten() throws {
        let j = try journal()
        XCTAssertTrue(try j.parts(assetID: "A").isEmpty)
        try j.recordPart(assetID: "A", part: .original, sha: "s1", serverName: "IMG_1.HEIC")
        try j.recordPart(assetID: "A", part: .edit, sha: "s2", serverName: "IMG_1_E.jpg")
        try j.recordPart(assetID: "A", part: .edit, sha: "s3", serverName: "IMG_1_E.jpg")
        XCTAssertEqual(try j.parts(assetID: "A"), [
            .original: RecordedPart(sha: "s1", serverName: "IMG_1.HEIC"),
            .edit: RecordedPart(sha: "s3", serverName: "IMG_1_E.jpg"),
        ])
        try j.forgetPart(assetID: "A", part: .edit)
        XCTAssertEqual(try j.parts(assetID: "A").keys.sorted { $0.rawValue < $1.rawValue }, [.original])
        try j.wipe()
        XCTAssertTrue(try j.parts(assetID: "A").isEmpty)
    }

    /// Uploads made before parts existed are looked at once more: an edit may have gone up
    /// in place of the original, and no Live video went up at all.
    func testFormerUploadsComeBackOnce() throws {
        let db = try DatabaseQueue()
        let v = Date(timeIntervalSince1970: 1_000_000)
        do {
            let j = try journal(db)
            try j.markSent(assetID: "sent", modified: v, bytes: 9, sha: "H", serverName: "IMG_1.JPG")
            try j.markDeferred(assetID: "failed", modified: v, error: "offline")
            try j.seedPreexisting([(id: "old", modified: v)])
        }
        // A journal of version 1: no parts, no record of what is former.
        try db.write { db in
            try db.execute(sql: "DROP TABLE IF EXISTS upload_parts; DROP TABLE IF EXISTS legacy_uploads; PRAGMA user_version = 1")
        }
        let j = try journal(db)
        XCTAssertEqual(try j.legacyUpload(assetID: "sent"), LegacyUpload(modified: v, sha: "H", serverName: "IMG_1.JPG"))
        XCTAssertNil(try j.legacyUpload(assetID: "failed"))
        XCTAssertNil(try j.legacyUpload(assetID: "old"))
        XCTAssertFalse(try j.isKnown(assetID: "sent", modified: v), "a former upload is work again")
        XCTAssertTrue(try j.isKnown(assetID: "old", modified: v))
        XCTAssertEqual(try j.pendingPhotos(limit: 10).map(\.id), ["sent"])
        XCTAssertEqual(try j.counts().sent, 1, "it stays counted as sent")

        // Looked at: done, and opening the journal again does not bring it back.
        try j.forgetLegacy(assetID: "sent")
        try j.markSent(assetID: "sent", modified: v, bytes: 9, sha: "H", serverName: "IMG_1.HEIC")
        XCTAssertTrue(try j.isKnown(assetID: "sent", modified: v))
        let again = try journal(db)
        XCTAssertNil(try again.legacyUpload(assetID: "sent"))
        XCTAssertTrue(try again.isKnown(assetID: "sent", modified: v))
    }

    /// A transfer queued by the former version finishes after the update: it is a former
    /// upload too, and its failure must not erase what is known about it.
    func testLegacyTransferIsRecordedAsFormer() throws {
        let j = try journal()
        let v = Date(timeIntervalSince1970: 5)
        try j.markSent(assetID: "Q", modified: v, bytes: 3, sha: "S", serverName: "IMG_9.JPG", legacy: true)
        XCTAssertEqual(try j.legacyUpload(assetID: "Q"), LegacyUpload(modified: v, sha: "S", serverName: "IMG_9.JPG"))
        XCTAssertFalse(try j.isKnown(assetID: "Q", modified: v))
        try j.markDeferred(assetID: "Q", modified: v, error: "offline")
        XCTAssertEqual(try j.legacyUpload(assetID: "Q")?.sha, "S")
    }

    // MARK: transfer queue

    private func exported(_ root: URL, _ bytes: [UInt8]) throws -> URL {
        let file = root.appendingPathComponent(UUID().uuidString)
        try Data(bytes).write(to: file)
        return file
    }

    func testPartsOfOneAssetQueueTogether() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let store = try PhotoTransferStore(directory: root)
        let v = Date(timeIntervalSince1970: 7)
        func job(_ part: PhotoPart, _ name: String) -> PhotoTransfer {
            PhotoTransfer(account: "a", assetID: "A", modified: v, created: v, parentID: "p",
                          name: name, sha: name, bytes: 2, part: part)
        }
        let original = job(.original, "IMG_1.HEIC"), edit = job(.edit, "IMG_1_E.jpg"), live = job(.live, "IMG_1.MOV")
        try store.stage([(original, try exported(root, [1, 2])), (edit, try exported(root, [3, 4])),
                         (live, try exported(root, [5, 6]))])
        XCTAssertEqual(try store.all(account: "a").map(\.part), [.original, .edit, .live])
        XCTAssertEqual(try Data(contentsOf: store.assetURL(edit)), Data([3, 4]))

        // The same part twice is refused, and nothing of that batch stays behind.
        let again = job(.live, "IMG_1.MOV"), extra = job(.edit, "x")
        let files = [try exported(root, [7]), try exported(root, [8])]
        XCTAssertThrowsError(try store.stage([(extra, files[0]), (again, files[1])]))
        XCTAssertEqual(try store.all(account: "a").count, 3)
        XCTAssertFalse(FileManager.default.fileExists(atPath: store.assetURL(extra).path))
    }

    /// A queue written by the former version (one job per asset version, no part) opens,
    /// keeps its jobs, and then takes parts.
    func testFormerQueueOpens() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        let v = Date(timeIntervalSince1970: 9)
        var former = PhotoTransfer(account: "a", assetID: "A", modified: v, created: v, parentID: "p",
                                   name: "IMG_1.JPG", sha: "s", bytes: 1)
        former.uploadID = "u1"
        var payload = try JSONSerialization.jsonObject(with: JSONEncoder().encode(former)) as! [String: Any]
        payload.removeValue(forKey: "part"); payload.removeValue(forKey: "replaces")
        let blob = try JSONSerialization.data(withJSONObject: payload)
        let db = try DatabaseQueue(path: root.appendingPathComponent("queue.sqlite").path)
        try db.write { db in
            try db.execute(sql: """
                CREATE TABLE photo_transfers (
                    id TEXT PRIMARY KEY, account TEXT NOT NULL, asset TEXT NOT NULL,
                    modified INTEGER NOT NULL, payload BLOB NOT NULL,
                    UNIQUE(account, asset, modified)
                )
                """)
            try db.execute(sql: "INSERT INTO photo_transfers VALUES (?, 'a', 'A', 9000, ?)", arguments: [former.id, blob])
        }
        try db.close()

        let store = try PhotoTransferStore(directory: root)
        let restored = try XCTUnwrap(store.all(account: "a").first)
        XCTAssertNil(restored.part)
        XCTAssertEqual(restored.uploadID, "u1")
        XCTAssertFalse(restored.replacesFile)
        let live = PhotoTransfer(account: "a", assetID: "A", modified: v, created: v, parentID: "p",
                                 name: "IMG_1.MOV", sha: "m", bytes: 1, part: .live)
        try store.stage([(live, try exported(root, [1]))])
        XCTAssertEqual(try store.all(account: "a").count, 2)
    }
}
