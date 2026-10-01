import XCTest
import DiscoKit
@testable import DiscoDrive

/// A pass that is still waiting for the network when the user logs out must not come back
/// afterwards and seed the wiped journal (or upload) with the account that was left.
@MainActor
final class AutoUploadLogoutTests: XCTestCase {
    func testLogoutWhileAPassWaitsForTheNetworkLeavesNothingBehind() async throws {
        let service = AutoUploadService.shared
        let settings = AutoUploadSettings.shared
        let savedWait = service.waitForPath, savedCheck = service.blockingCondition
        defer { service.waitForPath = savedWait; service.blockingCondition = savedCheck; settings.reset() }
        settings.reset()
        settings.enabled = true
        settings.seeded = false
        service.blockingCondition = { _ in .none }   // the simulator's network is not the point
        service.configure(apiProvider: {
            APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test")
        })
        // Hold the pass at its first suspension point until released.
        let (gate, open) = AsyncStream<Void>.makeStream()
        service.waitForPath = { for await _ in gate { return } }

        let pass = Task { await service.runPass() }
        while !service.running { await Task.yield() }
        let logout = Task { await service.logout() }
        for _ in 0..<20 { await Task.yield() }   // logout gets to its wait
        open.yield(())
        open.finish()
        _ = await pass.value
        await logout.value

        XCTAssertFalse(settings.seeded, "the pass must not re-seed after logout")
        XCTAssertFalse(settings.enabled)
        XCTAssertEqual(try service.openJournal().recent(limit: 1).count, 0, "the journal stays empty")
        XCTAssertFalse(service.running)
    }
}
