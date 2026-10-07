import XCTest
import DiscoKit
import BackgroundTasks
@testable import DiscoDrive

@MainActor
final class AutoUploadResumeTests: XCTestCase {
    func testResumeStartsPassWithoutPhotoLibraryNotification() async {
        let service = AutoUploadService.shared
        let settings = AutoUploadSettings.shared
        let wait = service.waitForPath, check = service.blockingCondition
        defer { service.waitForPath = wait; service.blockingCondition = check; settings.reset() }
        settings.reset()
        settings.enabled = true
        service.configure { APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test") }
        let started = expectation(description: "foreground catch-up")
        service.waitForPath = { started.fulfill() }
        service.blockingCondition = { _ in .noNetwork }
        service.resumeIfEnabled()
        await fulfillment(of: [started], timeout: 2)
        while service.running { await Task.yield() }
        XCTAssertEqual(service.lastResult?.blocked, .noNetwork)
        await service.setEnabled(false)
    }

    func testTriggerDuringPassIsCoalescedAndAwaited() async {
        let service = AutoUploadService.shared
        let settings = AutoUploadSettings.shared
        let wait = service.waitForPath, check = service.blockingCondition
        defer { service.waitForPath = wait; service.blockingCondition = check; settings.reset() }
        settings.reset()
        settings.enabled = true
        service.configure { APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test") }
        let (gate, open) = AsyncStream<Void>.makeStream()
        var passes = 0
        service.waitForPath = {
            passes += 1
            if passes == 1 { for await _ in gate { return } }
        }
        service.blockingCondition = { _ in .noNetwork }
        let first = Task { await service.runPass() }
        while passes == 0 { await Task.yield() }
        let second = Task { await service.runPass() }
        // Main-actor FIFO: the second request reaches the owner before the gate opens.
        await Task.yield()
        open.yield(())
        open.finish()
        let joined = await second.value
        _ = await first.value
        XCTAssertEqual(passes, 2)
        XCTAssertEqual(joined.blocked, .noNetwork)
        XCTAssertFalse(service.running)
    }

    func testConsumedBackgroundRequestGetsSuccessorWithoutPostponingOtherRequest() async {
        let service = AutoUploadService.shared
        let settings = AutoUploadSettings.shared
        let pending = service.pendingBackgroundRequests, submit = service.submitBackgroundRequest
        defer { service.pendingBackgroundRequests = pending; service.submitBackgroundRequest = submit; settings.reset() }
        settings.enabled = true
        let processing = BGProcessingTaskRequest(identifier: AutoUploadService.taskID)
        processing.earliestBeginDate = Date(timeIntervalSinceNow: 600)
        let refresh = BGAppRefreshTaskRequest(identifier: AutoUploadService.refreshTaskID)
        service.pendingBackgroundRequests = { [processing, refresh] }
        let successor = expectation(description: "refresh successor")
        service.submitBackgroundRequest = { request in
            XCTAssertEqual(request.identifier, AutoUploadService.refreshTaskID)
            XCTAssertGreaterThan(request.earliestBeginDate ?? .distantPast, Date())
            successor.fulfill()
        }
        service.scheduleBackgroundPass(replacing: AutoUploadService.refreshTaskID)
        await fulfillment(of: [successor], timeout: 2)
    }

    func testExpiringOneBackgroundLeaseDoesNotCancelAnotherOwnersPass() async {
        await exerciseLeaseExpiry(expireLast: false)
    }

    func testExpiringLastBackgroundLeaseCancelsPass() async {
        await exerciseLeaseExpiry(expireLast: true)
    }

    private func exerciseLeaseExpiry(expireLast: Bool) async {
        let service = AutoUploadService.shared
        let settings = AutoUploadSettings.shared
        let wait = service.waitForPath, check = service.blockingCondition
        defer { service.waitForPath = wait; service.blockingCondition = check; settings.reset() }
        settings.enabled = true
        service.configure { APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test") }
        let (gate, open) = AsyncStream<Void>.makeStream()
        var waiting = false
        var cancelled = false
        service.waitForPath = {
            waiting = true
            for await _ in gate { break }
            cancelled = Task.isCancelled
        }
        service.blockingCondition = { _ in .noNetwork }
        let active = service.applicationIsActive
        service.applicationIsActive = { false }
        defer { service.applicationIsActive = active }
        let processing = service.retainBackgroundPass()
        let refresh = service.retainBackgroundPass()
        let work = Task { await service.runPass() }
        while !waiting { await Task.yield() }
        service.releaseBackgroundPass(processing, expired: true)
        if expireLast { service.releaseBackgroundPass(refresh, expired: true) }
        open.yield(()); open.finish()
        let result = await work.value
        service.releaseBackgroundPass(refresh, expired: false)
        XCTAssertEqual(cancelled, expireLast)
        XCTAssertEqual(result.blocked, expireLast ? .none : .noNetwork)
    }

}
