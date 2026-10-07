import XCTest
import Photos
import UIKit
import DiscoKit
@testable import DiscoDrive

@MainActor
final class PhotoKitDiscoveryTests: XCTestCase {
    func testNewPhotoIsDiscoveredThroughPersistentHistory() async throws {
        #if targetEnvironment(simulator)
        guard PHPhotoLibrary.authorizationStatus(for: .readWrite) == .authorized else {
            throw XCTSkip("Requires full Photos permission on the isolated test simulator")
        }
        let path = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString).path
        defer { try? FileManager.default.removeItem(atPath: path) }
        let journal = try UploadJournal(path: path)
        let source = CountingSource()
        try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: true)
        XCTAssertEqual(source.snapshots, 1)
        XCTAssertNotNil(try journal.photoDiscoveryCursor())
        let data = UIGraphicsImageRenderer(size: CGSize(width: 8, height: 8)).pngData { context in
            UIColor.green.setFill(); context.fill(CGRect(x: 0, y: 0, width: 8, height: 8))
        }
        try await PHPhotoLibrary.shared().performChanges { @Sendable in
            let request = PHAssetCreationRequest.forAsset()
            request.addResource(with: .photo, data: data, options: nil)
        }
        var found: [PhotoAssetStamp] = []
        for _ in 0..<20 {
            try PhotoDiscovery.reconcile(source: source, journal: journal, seedPreexisting: false)
            found = try journal.pendingPhotos(limit: 20)
            if !found.isEmpty { break }
            try await Task.sleep(for: .milliseconds(250))
        }
        XCTAssertEqual(found.count, 1)
        XCTAssertEqual(source.snapshots, 1, "New media must use history, not a second full scan")
        XCTAssertGreaterThan(source.historyCalls, 0)
        #else
        throw XCTSkip("Creates test media only on an isolated simulator, never on a personal phone")
        #endif
    }
}

private final class CountingSource: PhotoDiscoverySource {
    var snapshots = 0
    var historyCalls = 0
    func snapshot() throws -> PhotoLibraryDelta {
        snapshots += 1
        return try PhotoKitDiscoverySource().snapshot()
    }
    func changes(since: Data, consume: (PhotoLibraryDelta) throws -> Void) throws {
        historyCalls += 1
        try PhotoKitDiscoverySource().changes(since: since, consume: consume)
    }
}
