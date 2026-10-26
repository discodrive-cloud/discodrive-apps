import XCTest
@testable import DiscoKit

final class UploadProblemTests: XCTestCase {
    func testClassifiesDiskFullWhereverTheErrorWrapsIt() {
        let posix = NSError(domain: NSPOSIXErrorDomain, code: Int(ENOSPC))
        let cocoa = NSError(domain: NSCocoaErrorDomain, code: NSFileWriteOutOfSpaceError,
                            userInfo: [NSUnderlyingErrorKey: posix])
        XCTAssertEqual(UploadProblem.classify(cocoa), .diskFull)
        XCTAssertEqual(UploadProblem.classify(posix), .diskFull)
        XCTAssertEqual(UploadProblem.classify(CocoaError(.fileWriteOutOfSpace)), .diskFull)
        let wrapped = NSError(domain: NSCocoaErrorDomain, code: NSFileWriteUnknownError,
                              userInfo: [NSUnderlyingErrorKey: posix])
        XCTAssertEqual(UploadProblem.classify(wrapped), .diskFull)
    }

    func testClassifiesNetworkServerAuthAndTheRest() {
        XCTAssertEqual(UploadProblem.classify(URLError(.timedOut)), .network)
        XCTAssertEqual(UploadProblem.classify(URLError(.notConnectedToInternet)), .network)
        XCTAssertEqual(UploadProblem.classify(URLError(.networkConnectionLost)), .network)
        XCTAssertEqual(UploadProblem.classify(URLError(.cannotConnectToHost)), .network)
        XCTAssertEqual(UploadProblem.classify(APIError.http(502)), .server)
        XCTAssertEqual(UploadProblem.classify(APIError.http(401)), .auth)
        XCTAssertEqual(UploadProblem.classify(APIError.notAuthenticated), .auth)
        XCTAssertEqual(UploadProblem.classify(APIError.http(409)), .other)
        XCTAssertEqual(UploadProblem.classify(APIError.badResponse), .other)
    }

    func testEveryProblemHasATranslatedMessage() {
        for problem in UploadProblem.allCases {
            for lang in L10n.supported {
                let text = L10n.t(problem.messageKey, lang)
                XCTAssertNotEqual(text, problem.messageKey, "\(problem) has no text")
                XCTAssertFalse(text.isEmpty)
            }
        }
        for lang in L10n.supported {
            XCTAssertNotEqual(L10n.t("au.blocked.storage", lang), "au.blocked.storage")
        }
    }

    func testDeferRetryRemembersTheKindAndSuccessForgetsIt() {
        let now = Date(timeIntervalSince1970: 1000)
        var job = PhotoTransfer(account: "a", assetID: "p", modified: now, created: now,
                                parentID: "f", name: "a.jpg", sha: "s", bytes: 1)
        job.deferRetry(NSError(domain: NSPOSIXErrorDomain, code: Int(ENOSPC)), now: now)
        XCTAssertEqual(job.problem, .diskFull)
        XCTAssertEqual(job.failedAt, now)
        XCTAssertNotNil(job.lastError)
        XCTAssertEqual(job.attempts, 1)
        job.clearFailure()
        XCTAssertNil(job.problem); XCTAssertNil(job.failedAt); XCTAssertNil(job.lastError)
        XCTAssertEqual(job.attempts, 0)
        XCTAssertEqual(job.retryAt, .distantPast)
    }

    func testRecordsWrittenBeforeTheFieldsExistedStillDecode() throws {
        let legacy = """
        {"id":"x","account":"a","assetID":"p","modified":0,"created":0,"parentID":"f","name":"n",
         "sha":"s","bytes":1,"nextChunk":0,"attempts":2,"retryAt":0,"lastError":"boom"}
        """
        let job = try JSONDecoder().decode(PhotoTransfer.self, from: Data(legacy.utf8))
        XCTAssertNil(job.problem)
        XCTAssertEqual(job.lastError, "boom")
    }

    private func failed(_ problem: UploadProblem, at seconds: TimeInterval, attempts: Int = 1) -> PhotoTransfer {
        var job = PhotoTransfer(account: "a", assetID: UUID().uuidString, modified: .distantPast,
                                created: .distantPast, parentID: "f", name: "n", sha: "s", bytes: 1)
        job.problem = problem; job.failedAt = Date(timeIntervalSince1970: seconds)
        job.attempts = attempts; job.lastError = "raw"
        return job
    }

    func testVisibleProblemHidesWhatLaterProgressDisproved() {
        let stale = failed(.diskFull, at: 100)
        XCTAssertEqual(PhotoTransfer.visibleProblem(in: [stale], lastProgress: nil), .diskFull)
        XCTAssertEqual(PhotoTransfer.visibleProblem(in: [stale], lastProgress: Date(timeIntervalSince1970: 50)), .diskFull)
        XCTAssertNil(PhotoTransfer.visibleProblem(in: [stale], lastProgress: Date(timeIntervalSince1970: 150)))
    }

    func testVisibleProblemPrefersTheNewestAndKeepsOneOffNetworkHiccupsQuiet() {
        XCTAssertNil(PhotoTransfer.visibleProblem(in: [failed(.network, at: 100, attempts: 1)], lastProgress: nil))
        XCTAssertEqual(PhotoTransfer.visibleProblem(in: [failed(.network, at: 100, attempts: 3)], lastProgress: nil), .network)
        let jobs = [failed(.server, at: 100), failed(.diskFull, at: 200), failed(.network, at: 300, attempts: 1)]
        XCTAssertEqual(PhotoTransfer.visibleProblem(in: jobs, lastProgress: nil), .diskFull)
        // A record from an older build carries only the raw text: still worth a generic line.
        var legacy = failed(.other, at: 0); legacy.problem = nil; legacy.failedAt = nil
        XCTAssertEqual(PhotoTransfer.visibleProblem(in: [legacy], lastProgress: nil), .other)
    }

    func testStagingBudget() {
        let gib: Int64 = 1 << 30, mib: Int64 = 1 << 20
        // The first item always gets a chance as long as the device has a working margin.
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 0, queuedCount: 0, freeBytes: 10 * gib), .export)
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 0, queuedCount: 0, freeBytes: 100 * mib), .lowSpace)
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 0, queuedCount: 0, freeBytes: nil), .export)
        // Copies already waiting cap the queue by size, not by count.
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 900 * mib, queuedCount: 9, freeBytes: 10 * gib), .export)
        XCTAssertEqual(StagingBudget.decide(stagedBytes: gib, queuedCount: 10, freeBytes: 10 * gib), .queueFull)
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 5 * gib, queuedCount: 1, freeBytes: 10 * gib), .queueFull)
        XCTAssertEqual(StagingBudget.decide(stagedBytes: 100 * mib, queuedCount: 1, freeBytes: 100 * mib), .lowSpace)
    }
}
