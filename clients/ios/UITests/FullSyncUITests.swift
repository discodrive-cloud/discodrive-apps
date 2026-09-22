import XCTest

@MainActor
final class FullSyncUITests: XCTestCase {
    func testEnableSyncAndRestoreItAfterRelaunch() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        let server = env["DD_TEST_SERVER"] ?? ""
        let token = env["DD_TEST_TOKEN"] ?? ""
        try XCTSkipIf(server.isEmpty || token.isEmpty, "Needs an isolated sync fixture server")
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = server
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = token
        app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings"
        app.launch()
        let toggle = app.switches.firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        if toggle.value as? String == "0" { toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap() }
        XCTAssertTrue(app.staticTexts["Up to date"].waitForExistence(timeout: 30))
        app.terminate()
        app.launch()
        XCTAssertTrue(toggle.waitForExistence(timeout: 20))
        XCTAssertEqual(toggle.value as? String, "1")
        XCTAssertTrue(app.staticTexts["Up to date"].waitForExistence(timeout: 30))
        XCUIDevice.shared.press(.home)
        app.activate()
        XCTAssertTrue(app.staticTexts["Up to date"].waitForExistence(timeout: 30))
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        XCTAssertEqual(toggle.value as? String, "0")
    }
    func testFilesShowsRemoteFile() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        try XCTSkipIf(env["DD_TEST_SERVER"] == nil || env["DD_TEST_TOKEN"] == nil, "Needs an isolated sync fixture server")
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = env["DD_TEST_SERVER"]
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = env["DD_TEST_TOKEN"]
        app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings"
        app.launch()
        XCTAssertTrue(app.staticTexts["DiscoDrive is connected"].waitForExistence(timeout: 20))
        let files = XCUIApplication(bundleIdentifier: "com.apple.DocumentsApp")
        files.launch()
        let browse = files.buttons["Browse"].firstMatch
        XCTAssertTrue(browse.waitForExistence(timeout: 15))
        browse.tap()
        for _ in 0..<5 {
            let back = files.buttons["BackButton"]
            if back.exists { back.tap() } else { break }
        }
        let location = files.staticTexts["DiscoDrive"].firstMatch
        XCTAssertTrue(location.waitForExistence(timeout: 15))
        location.tap()
        XCTAssertTrue(files.staticTexts["remote"].firstMatch.waitForExistence(timeout: 20)
            || files.staticTexts["remote.md"].firstMatch.exists)
    }

}
