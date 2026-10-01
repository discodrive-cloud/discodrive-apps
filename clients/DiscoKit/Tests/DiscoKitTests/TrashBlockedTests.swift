import XCTest
@testable import DiscoKit

// The server keeps a trashed folder that still holds a live item and answers purge / empty
// trash with 409. The client reports that as .trashBlocked, to explain it in words; other
// failures of the same calls keep their status.
final class TrashBlockedTests: XCTestCase {
    private func client(_ status: Int) -> APIClient {
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("/auth/device/token") {
                return (200, ["Content-Type": "application/json"], try! JSONSerialization.data(withJSONObject: ["token": "jwt"]))
            }
            return (status, ["Content-Type": "application/json"],
                    try! JSONSerialization.data(withJSONObject: ["error": "folder holds items that are not in the trash"]))
        }
        return APIClient(baseURL: URL(string: "https://x.test")!, deviceToken: "D", session: MockURLProtocol.session())
    }

    func testPurgeAndEmptyTrashReportAConflictAsTrashBlocked() async throws {
        let api = client(409)
        do { try await api.purge(id: "t1"); XCTFail("want trashBlocked") } catch APIError.trashBlocked {}
        do { try await api.emptyTrash(); XCTFail("want trashBlocked") } catch APIError.trashBlocked {}
    }

    func testOtherFailuresKeepTheirStatus() async throws {
        let api = client(500)
        do { try await api.purge(id: "t1"); XCTFail("want a 500") } catch APIError.http(500) {}
    }

    func testTheMessageIsLocalized() {
        for lang in L10n.supported {
            XCTAssertFalse(L10n.table["recovery.trashBlocked"]?[lang]?.isEmpty ?? true, "recovery.trashBlocked missing in \(lang)")
        }
    }
}
