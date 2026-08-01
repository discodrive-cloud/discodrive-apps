import XCTest

/// End-to-end check for auto-upload against a real server.
///
/// It exists because the interesting parts are exactly the ones a unit test cannot reach:
/// the photo-library permission dialog, PhotoKit handing back an original, and the upload
/// actually landing. The simulator cannot be tapped from a shell, so this drives it.
///
/// Needs a server to talk to; skipped unless the variables are set. They must be passed
/// with the TEST_RUNNER_ prefix, or xcodebuild keeps them to itself:
///
///   TEST_RUNNER_DD_TEST_SERVER=http://localhost:8080 TEST_RUNNER_DD_TEST_TOKEN=kfd_… \
///     xcodebuild test -scheme DiscoDrive -destination 'platform=iOS Simulator,name=iPhone 17 Pro'
final class AutoUploadUITests: XCTestCase {

    private var server: String { ProcessInfo.processInfo.environment["DD_TEST_SERVER"] ?? "" }
    private var token: String { ProcessInfo.processInfo.environment["DD_TEST_TOKEN"] ?? "" }

    override func setUpWithError() throws {
        continueAfterFailure = false
        try XCTSkipIf(server.isEmpty || token.isEmpty,
                      "set TEST_RUNNER_DD_TEST_SERVER and TEST_RUNNER_DD_TEST_TOKEN to run this")
    }

    private func launchApp(screen: String? = nil) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = server
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = token
        if let screen { app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = screen }
        app.launch()
        return app
    }

    /// Answers the photo-library prompt.
    ///
    /// The alert belongs to SpringBoard, and on this iOS version its buttons only resolve
    /// once it is actually on screen — so this polls rather than asking a process that may
    /// not be running yet.
    @discardableResult
    private func allowPhotos() -> Bool {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let wanted = ["Allow Full Access", "Allow Access to All Photos", "Allow", "OK"]
        let deadline = Date().addingTimeInterval(25)
        while Date() < deadline {
            for label in wanted {
                let button = springboard.buttons[label]
                if button.exists {
                    button.tap()
                    return true
                }
            }
            // Also check the alert surface the app itself may own.
            let alert = XCUIApplication().alerts.firstMatch
            if alert.exists {
                for label in wanted where alert.buttons[label].exists {
                    alert.buttons[label].tap()
                    return true
                }
            }
            usleep(1_000_000)
        }
        var seen: [String] = []
        for b in springboard.buttons.allElementsBoundByIndex { seen.append(b.label) }
        print("PHOTO_PROMPT_NOT_FOUND springboard buttons: \(seen)")
        return false
    }

    /// Polls a condition instead of XCTestExpectation: the predicate form captures the test
    /// case, which strict concurrency refuses.
    private func waitUntil(timeout: TimeInterval, _ condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return true }
            usleep(500_000)
        }
        return condition()
    }

    /// Reads "Sent 3 · skipped 11 · deferred 0" off the screen.
    private func counters(_ app: XCUIApplication) -> (sent: Int, skipped: Int, deferred: Int)? {
        let label = app.staticTexts.containing(
            NSPredicate(format: "label CONTAINS[c] 'skipped' OR label CONTAINS[c] 'пропущено'")
        ).firstMatch
        guard label.waitForExistence(timeout: 30) else { return nil }
        let numbers = label.label.split(whereSeparator: { !$0.isNumber }).compactMap { Int($0) }
        guard numbers.count >= 3 else { return nil }
        return (numbers[0], numbers[1], numbers[2])
    }

    /// Switching it on must record the existing library instead of uploading it — the whole
    /// point of seeding. A pass that uploads here would push years of photos over cellular.
    func testEnablingSeedsTheLibraryInsteadOfUploadingIt() throws {
        let app = launchApp(screen: "autoupload")
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), "the auto-upload screen should be up")
        XCTAssertEqual(toggle.value as? String, "0", "auto-upload must start off")

        toggle.tap()
        XCTAssertTrue(allowPhotos(), "the photo-library prompt must be answered")

        // The switch has to stay on: it flips back when access was refused.
        XCTAssertTrue(waitUntil(timeout: 30) { toggle.value as? String == "1" },
                      "the switch must stay on once access is granted")

        guard let counts = counters(app) else {
            return XCTFail("counters never appeared")
        }
        XCTAssertGreaterThan(counts.skipped, 0, "the existing library must be recorded as pre-existing")
        XCTAssertEqual(counts.sent, 0, "switching on must not upload what was already there")
    }

    /// A photo added after the feature was switched on is what auto-upload is for.
    func testNewPhotoIsUploaded() throws {
        let app = launchApp(screen: "autoupload")
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        if toggle.value as? String == "0" {
            toggle.tap()
            allowPhotos()
            XCTAssertTrue(waitUntil(timeout: 30) { toggle.value as? String == "1" })
        }
        let before = counters(app) ?? (0, 0, 0)

        // The harness drops a fresh photo into the library between the two runs; this test
        // only asserts that a pass moves the "sent" counter when there is something new.
        let uploadNow = app.buttons.matching(
            NSPredicate(format: "label CONTAINS[c] 'Upload now' OR label CONTAINS[c] 'Загрузить'")
        ).firstMatch
        XCTAssertTrue(uploadNow.waitForExistence(timeout: 10))
        uploadNow.tap()

        let deadline = Date().addingTimeInterval(90)
        var after = before
        while Date() < deadline {
            after = counters(app) ?? after
            if after.sent > before.sent { break }
            usleep(2_000_000)
        }
        XCTAssertGreaterThan(after.sent, before.sent,
                             "a new photo must be uploaded when one is waiting")
    }

    /// The screen has to be usable before anything is switched on: this is what a user sees
    /// first, and an empty state that shows nothing would be the worst welcome.
    func testScreenRendersBeforeEnabling() throws {
        let app = launchApp(screen: "autoupload")
        XCTAssertTrue(app.switches.firstMatch.waitForExistence(timeout: 20))
        XCTAssertTrue(app.staticTexts.containing(
            NSPredicate(format: "label CONTAINS[c] 'Camera Uploads'")).firstMatch.exists,
            "the destination path should be visible up front")
    }
}
