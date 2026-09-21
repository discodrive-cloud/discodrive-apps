import XCTest
@testable import DiscoKit

@MainActor
final class AccountSessionTests: XCTestCase {
    private actor Transfer {
        private var response: CheckedContinuation<Void, Never>?
        private var started: CheckedContinuation<Void, Never>?
        func download() async -> Data {
            await withCheckedContinuation { continuation in
                response = continuation
                started?.resume(); started = nil
            }
            // Model a transport which ignores cancellation and returns successful bytes.
            return Data("old account".utf8)
        }
        func waitUntilStarted() async {
            if response != nil { return }
            await withCheckedContinuation { started = $0 }
        }
        func finish() { response?.resume(); response = nil }
    }

    func testLateDownloadCannotRemoveNewAccountsFile() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let directory = root.appendingPathComponent("local")
        let oldLocal = try LocalStore(directory: directory)
        let session = AccountSession()
        let transfer = Transfer()
        let download = Task { () -> Bool in
            do {
                let bytes = try await session.perform { await transfer.download() }
                try session.check()
                let tmp = root.appendingPathComponent("old.tmp")
                try bytes.write(to: tmp)
                try oldLocal.store(nodeID: "old", version: 1, from: tmp, pinned: false, relPath: "same.txt")
                return true
            } catch { return false }
        }
        await transfer.waitUntilStarted()
        session.invalidate()
        try FileManager.default.removeItem(at: directory)
        let newLocal = try LocalStore(directory: directory)
        let tmp = root.appendingPathComponent("new.tmp")
        try Data("new account".utf8).write(to: tmp)
        try newLocal.store(nodeID: "new", version: 1, from: tmp, pinned: true, relPath: "same.txt")
        await transfer.finish()
        let wroteOldBytes = await download.value
        XCTAssertFalse(wroteOldBytes)
        let kept = try XCTUnwrap(newLocal.localURL(nodeID: "new"))
        XCTAssertEqual(try Data(contentsOf: kept), Data("new account".utf8))
        XCTAssertEqual(try newLocal.unregisteredFiles(), [])
    }

    func testStopWaitsForOutstandingWorkAndRejectsNewWork() async throws {
        let session = AccountSession()
        let transfer = Transfer()
        let job = Task { try? await session.perform { await transfer.download() } }
        await transfer.waitUntilStarted()
        var stopped = false
        let stop = Task { await session.stop(); stopped = true }
        while session.isActive { await Task.yield() }
        XCTAssertFalse(stopped, "cleanup cannot run until the outstanding work settles")
        do {
            _ = try await session.perform { 42 }
            XCTFail("an invalidated account cannot start requests")
        } catch is CancellationError {} catch { XCTFail("unexpected error: \(error)") }
        await transfer.finish()
        await stop.value
        _ = await job.value
        XCTAssertTrue(stopped)
        let replacement = AccountSession()
        let value = try await replacement.perform { 42 }
        XCTAssertEqual(value, 42)
    }
}
