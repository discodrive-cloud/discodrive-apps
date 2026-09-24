import XCTest

@MainActor
final class DAVSetupUITests: XCTestCase {
    func testPrepareAndRevokeSystemAccountPassword() throws {
        let environment = ProcessInfo.processInfo.environment
        try XCTSkipIf(environment["DD_TEST_SERVER"] == nil, "Requires the isolated DAV fixture")
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = environment["DD_TEST_SERVER"]
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = environment["DD_TEST_TOKEN"]
        app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings"
        app.launch()
        let setup = app.buttons["Calendars and contacts"]
        XCTAssertTrue(setup.waitForExistence(timeout: 20)); setup.tap()
        let prepare = app.buttons["Connect with password entry"]
        XCTAssertTrue(prepare.waitForExistence(timeout: 10))
        for _ in 0..<20 { if prepare.isEnabled { break }; Thread.sleep(forTimeInterval: 0.25) }
        XCTAssertTrue(prepare.isEnabled); prepare.tap()
        XCTAssertTrue(app.buttons["Copy connection password"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["Download installation profile"].exists)
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = "dav-setup"; screenshot.lifetime = .keepAlways; add(screenshot)
        let revoke = app.buttons["Revoke connection password"]
        for _ in 0..<3 { if revoke.isHittable { break }; app.swipeUp() }
        revoke.tap()
        XCTAssertTrue(app.alerts.firstMatch.waitForExistence(timeout: 5))
        app.alerts.buttons["Revoke connection password"].tap()
        XCTAssertTrue(prepare.waitForExistence(timeout: 5))
    }
}
