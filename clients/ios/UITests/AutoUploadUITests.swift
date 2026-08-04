import XCTest

/// End-to-end checks for auto-upload against a real server.
///
/// They exist because the interesting parts are exactly the ones a unit test cannot reach:
/// the photo-library permission, PhotoKit handing back an original, and the upload landing.
///
/// **One manual tap.** iOS 26 asks for full photo-library access with a system alert that
/// automation cannot answer — `simctl privacy grant photos` does not cover it, and the
/// alert's buttons never resolve for XCUITest (SpringBoard reports none). `test1_…` waits
/// up to four minutes for a human to tap **Allow Full Access**; the rest runs unattended.
///
/// Drive them through `scripts/ios-e2e.sh`, which pairs the app, orders the two phases and
/// drops a fresh photo into the library between them.
final class AutoUploadUITests: XCTestCase {

    private var server: String { ProcessInfo.processInfo.environment["DD_TEST_SERVER"] ?? "" }
    private var token: String { ProcessInfo.processInfo.environment["DD_TEST_TOKEN"] ?? "" }

    override func setUpWithError() throws {
        continueAfterFailure = false
        try XCTSkipIf(server.isEmpty || token.isEmpty,
                      "set TEST_RUNNER_DD_TEST_SERVER and TEST_RUNNER_DD_TEST_TOKEN to run this")
    }

    private func launchApp() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = server
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = token
        app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "autoupload"
        app.launch()
        return app
    }

    /// Polls instead of using an XCTestExpectation predicate: the predicate form captures
    /// the test case, which strict concurrency refuses.
    private func waitUntil(timeout: TimeInterval, _ condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return true }
            usleep(500_000)
        }
        return condition()
    }

    /// Reads "Sent 3 · skipped 11 · deferred 0" off the screen.
    private func counters(_ app: XCUIApplication, timeout: TimeInterval = 30)
        -> (sent: Int, skipped: Int, deferred: Int)? {
        let label = app.staticTexts.containing(
            NSPredicate(format: "label CONTAINS[c] 'skipped' OR label CONTAINS[c] 'пропущено'")
        ).firstMatch
        guard label.waitForExistence(timeout: timeout) else { return nil }
        let numbers = label.label.split(whereSeparator: { !$0.isNumber }).compactMap { Int($0) }
        guard numbers.count >= 3 else { return nil }
        return (numbers[0], numbers[1], numbers[2])
    }

    /// Wanting the existing library on your own server is the point of running one, so the
    /// archive that seeding set aside has to be reachable on purpose — and the photos that
    /// already went up must not go a second time.
    func test3_BackfillUploadsTheExistingLibrary() throws {
        let app = launchApp()
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 30))
        XCTAssertEqual(toggle.value as? String, "1", "run the earlier phases first")

        guard let before = counters(app) else { return XCTFail("counters never appeared") }
        try XCTSkipIf(before.skipped == 0, "nothing is set aside — nothing to back-fill")

        let backfill = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'existing' OR label CONTAINS[c] 'старые'")
        ).firstMatch
        XCTAssertTrue(backfill.waitForExistence(timeout: 15), "the back-fill button should be offered")
        backfill.tap()

        // The confirmation spells out the cost; accepting it is what starts the work.
        let confirm = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'Upload them' OR label CONTAINS[c] 'Загрузить'")
        ).firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 10))
        confirm.tap()

        var after = before
        let moved = waitUntil(timeout: 300) {
            after = self.counters(app, timeout: 5) ?? after
            return after.sent > before.sent && after.skipped == 0
        }
        print("PHASE3 sent \(before.sent) → \(after.sent), skipped \(before.skipped) → \(after.skipped), deferred \(after.deferred)")
        XCTAssertTrue(moved, "the set-aside photos should have been uploaded")
        XCTAssertEqual(after.deferred, 0, "nothing should fail in a clean back-fill")
    }

    /// Stopping has to arrive between photos, not at the end of the queue: a back-fill of a
    /// few thousand pictures is precisely what someone needs to be able to interrupt.
    func test4_StopInterruptsAPassInFlight() throws {
        let app = launchApp()
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 30))
        XCTAssertEqual(toggle.value as? String, "1", "run the earlier phases first")

        guard let before = counters(app) else { return XCTFail("counters never appeared") }
        try XCTSkipIf(before.skipped == 0, "nothing set aside — nothing long enough to stop")

        let backfill = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'existing' OR label CONTAINS[c] 'старые'")
        ).firstMatch
        XCTAssertTrue(backfill.waitForExistence(timeout: 15))
        backfill.tap()
        let confirm = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'Upload them' OR label CONTAINS[c] 'Загрузить'")
        ).firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 10))
        confirm.tap()

        // Stop as soon as the button shows up — that is while a pass is genuinely running.
        let stop = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'Stop' OR label CONTAINS[c] 'Останов'")
        ).firstMatch
        guard stop.waitForExistence(timeout: 30) else {
            throw XCTSkip("the pass finished before it could be stopped — too few photos")
        }
        stop.tap()

        // Whatever went up stays up, and the queue must not keep draining afterwards.
        let settled = counters(app) ?? before
        usleep(8_000_000)
        let later = counters(app) ?? settled
        print("PHASE4 stopped at sent=\(settled.sent); eight seconds later sent=\(later.sent)")
        XCTAssertEqual(later.sent, settled.sent, "the queue kept going after Stop")
    }

    /// The screen has to be readable before anything is switched on — it is what a user sees
    /// first, and it needs no permission at all.
    func test0_ScreenRendersBeforeEnabling() throws {
        let app = launchApp()
        XCTAssertTrue(app.switches.firstMatch.waitForExistence(timeout: 30))
        XCTAssertTrue(app.staticTexts.containing(
            NSPredicate(format: "label CONTAINS[c] 'DeviceUploads'")).firstMatch.exists,
            "the destination path should be visible up front")
    }

    /// Switching auto-upload on must record the library that is already there instead of
    /// uploading it. Getting this wrong pushes years of photos over a cellular link the
    /// first time anyone tries the feature.
    ///
    /// This is the phase that needs the tap.
    func test1_EnablingSeedsTheLibraryInsteadOfUploadingIt() throws {
        let app = launchApp()
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 30), "the auto-upload screen should be up")
        XCTAssertEqual(toggle.value as? String, "0", "auto-upload must start off")

        toggle.tap()
        let started = Date()

        print("""

        ==================================================================
          TAP  "Allow Full Access"  IN THE SIMULATOR NOW
          (iOS will not let a test answer this one — waiting up to 4 min)
        ==================================================================

        """)

        // Waiting on the switch would prove nothing: SwiftUI flips it the moment it is
        // tapped, long before the permission is answered. The pass that follows the grant
        // is what leaves a mark — seeding records the library, so a non-zero counter is the
        // first honest evidence that access was given.
        var counts: (sent: Int, skipped: Int, deferred: Int) = (0, 0, 0)
        var lastNag = Date()
        let granted = waitUntil(timeout: 240) {
            counts = self.counters(app, timeout: 3) ?? counts
            if Date().timeIntervalSince(lastNag) > 15 {
                lastNag = Date()
                let left = Int(240 - Date().timeIntervalSince(started))
                print(">>> still waiting for \"Allow Full Access\" in the simulator (\(left)s left)")
            }
            return counts.skipped > 0 || counts.sent > 0
        }
        if !granted {
            XCTAssertEqual(toggle.value as? String, "1",
                           "the switch went back off — access was refused")
            return XCTFail("no pass ran within four minutes — was \"Allow Full Access\" tapped?")
        }
        print("PHASE1 sent=\(counts.sent) skipped=\(counts.skipped) deferred=\(counts.deferred)")
        XCTAssertGreaterThan(counts.skipped, 0,
                             "photos already in the library must be recorded, not uploaded")
        XCTAssertEqual(counts.sent, 0, "switching on must not upload what was already there")
    }

    /// A photo taken after the feature was switched on is the whole point. The harness adds
    /// one between the phases; this asserts a pass picks it up and that nothing is deferred.
    func test2_NewPhotoIsUploaded() throws {
        let app = launchApp()
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 30))
        XCTAssertEqual(toggle.value as? String, "1",
                       "phase 1 should have left auto-upload on — run it first")

        let before = counters(app) ?? (0, 0, 0)
        let uploadNow = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'Upload now' OR label CONTAINS[c] 'Загрузить'")
        ).firstMatch
        XCTAssertTrue(uploadNow.waitForExistence(timeout: 15))
        uploadNow.tap()

        var after = before
        let moved = waitUntil(timeout: 120) {
            after = self.counters(app, timeout: 5) ?? after
            return after.sent > before.sent
        }
        print("PHASE2 before sent=\(before.sent) → after sent=\(after.sent) deferred=\(after.deferred)")
        XCTAssertTrue(moved, "the photo added after enabling should have been uploaded")
        XCTAssertEqual(after.deferred, 0, "nothing should end up deferred in a clean run")
    }
}
