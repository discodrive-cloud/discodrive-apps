import XCTest

@MainActor
final class RecoveryUITests: XCTestCase {
    private func launch(settings: Bool = false) throws -> XCUIApplication {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        try XCTSkipIf(env["DD_TEST_SERVER"] == nil || env["DD_TEST_TOKEN"] == nil, "Needs the isolated recovery fixture")
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = env["DD_TEST_SERVER"]
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = env["DD_TEST_TOKEN"]
        if settings { app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings" }
        app.launch()
        return app
    }
    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
    }
    func testVersionRestoreRequiresConfirmation() throws {
        let app = try launch()
        let file = app.buttons.containing(.staticText, identifier: "remote.md").firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 20)); file.press(forDuration: 1)
        app.buttons["Version history"].tap()
        XCTAssertTrue(app.staticTexts["Version 1"].waitForExistence(timeout: 10))
        capture("version-history", app)
        app.buttons["Restore"].tap()
        XCTAssertTrue(app.alerts.firstMatch.waitForExistence(timeout: 5))
        app.alerts.buttons["Cancel"].tap()
        app.buttons["Restore"].tap()
        app.alerts.buttons["Restore"].tap()
        XCTAssertTrue(app.staticTexts["Restored on the server."].waitForExistence(timeout: 10))
    }
    func testTrashRestoration() throws {
        let app = try launch()
        let actions = app.buttons["browser.actions"]
        XCTAssertTrue(actions.waitForExistence(timeout: 20)); actions.tap()
        app.buttons["Trash"].tap()
        XCTAssertTrue(app.staticTexts["Deleted note.txt"].waitForExistence(timeout: 10))
        capture("trash", app)
        app.buttons["Restore"].tap()
        XCTAssertTrue(app.staticTexts["Trash is empty."].waitForExistence(timeout: 10))
    }
    func testTrashCanBeClosed() throws {
        let app = try launch()
        let actions = app.buttons["browser.actions"]
        XCTAssertTrue(actions.waitForExistence(timeout: 20)); actions.tap()
        app.buttons["Trash"].tap()
        let done = app.buttons["Done"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 10)); XCTAssertTrue(done.isHittable)
        capture("trash-close", app)
        done.tap()
        XCTAssertTrue(actions.waitForExistence(timeout: 5))
    }
    func testSyncActivityCanBeClosed() throws {
        let app = try launch(settings: true)
        let activity = app.buttons["Sync activity"]
        for _ in 0..<3 { if activity.isHittable { break }; app.swipeUp() }
        XCTAssertTrue(activity.waitForExistence(timeout: 10)); activity.tap()
        XCTAssertTrue(app.staticTexts["Completed operations"].waitForExistence(timeout: 10))
        capture("sync-activity", app)
        app.buttons["Done"].firstMatch.tap()
        XCTAssertTrue(activity.waitForExistence(timeout: 5))
    }
}
