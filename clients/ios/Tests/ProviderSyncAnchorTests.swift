import XCTest
import FileProvider
import DiscoKit

final class ProviderSyncAnchorTests: XCTestCase {
    func testLegacyAnchorRequestsMetadataReenumeration() {
        XCTAssertThrowsError(try ProviderSyncAnchor.decode(SyncAnchorCodec.encode(42))) {
            XCTAssertEqual(($0 as NSError).domain, NSFileProviderErrorDomain)
            XCTAssertEqual(($0 as NSError).code, NSFileProviderError.syncAnchorExpired.rawValue)
        }
    }

    func testCurrentAnchorPreservesCursor() throws {
        for cursor: Int64 in [0, 42, Int64.max] {
            XCTAssertEqual(try ProviderSyncAnchor.decode(ProviderSyncAnchor.encode(cursor)), cursor)
        }
        XCTAssertThrowsError(try ProviderSyncAnchor.decode(Data("ios-actions-3:bad".utf8)))
        XCTAssertThrowsError(try ProviderSyncAnchor.decode(Data("ios-actions-2:".utf8) + SyncAnchorCodec.encode(42)))
    }
}
