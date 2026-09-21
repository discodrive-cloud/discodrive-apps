import XCTest
@testable import DiscoKit

@MainActor
final class ProviderDomainLifecycleTests: XCTestCase {
    private enum Failure: Error { case unavailable }

    func testFailedRemovalBlocksLogoutAndKeepsKeys() async {
        var domains = ["DiscoDrive", "vault-a"]
        var removedKeys: [String] = []
        let closed = await ProviderDomainLifecycle.close(
            matching: { _ in true }, list: { domains },
            remove: { id in
                if id == "DiscoDrive" { throw Failure.unavailable }
                domains.removeAll { $0 == id }
            }, didRemove: { removedKeys.append($0) })
        XCTAssertFalse(closed)
        XCTAssertEqual(domains, ["DiscoDrive"])
        XCTAssertEqual(removedKeys, ["vault-a"])
    }

    func testSuccessfulRemovalStillRequiresSystemConfirmation() async {
        let closed = await ProviderDomainLifecycle.close(
            matching: { _ in true }, list: { ["DiscoDrive"] }, remove: { _ in })
        XCTAssertFalse(closed)
    }

    func testListingFailureIsNotSuccessfulLogout() async {
        let failedInitially = await ProviderDomainLifecycle.close(
            matching: { _ in true }, list: { throw Failure.unavailable }, remove: { _ in XCTFail() })
        XCTAssertFalse(failedInitially)
        var calls = 0
        let failedFinally = await ProviderDomainLifecycle.close(
            matching: { _ in true },
            list: { calls += 1; if calls > 1 { throw Failure.unavailable }; return ["DiscoDrive"] },
            remove: { _ in })
        XCTAssertFalse(failedFinally)
    }

    func testClosedDomainsPermitLogoutAndAllManagedDomainsAreSignalled() async throws {
        var domains = ["DiscoDrive", "vault-a", "vault-b", "unrelated"]
        let includes: (String) -> Bool = { $0 == "DiscoDrive" || $0.hasPrefix("vault-") }
        var notified: [String] = []
        try await ProviderDomainLifecycle.signal(matching: includes, list: { domains }, notify: { notified.append($0) })
        XCTAssertEqual(notified, ["DiscoDrive", "vault-a", "vault-b"])
        let closed = await ProviderDomainLifecycle.close(
            matching: includes, list: { domains }, remove: { id in domains.removeAll { $0 == id } })
        XCTAssertTrue(closed)
        XCTAssertEqual(domains, ["unrelated"])
    }
}
