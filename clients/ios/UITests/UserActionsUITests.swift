import XCTest

@MainActor
final class UserActionsUITests: XCTestCase {
    private func launch(settings: Bool = false) throws -> XCUIApplication {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        try XCTSkipIf(env["DD_TEST_SERVER"] == nil || env["DD_TEST_TOKEN"] == nil, "Needs action fixture with remote.md and a vault")
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SERVER"] = env["DD_TEST_SERVER"]
        app.launchEnvironment["DISCODRIVE_TEST_TOKEN"] = env["DD_TEST_TOKEN"]
        if settings { app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings" }
        app.launch(); return app
    }
    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot()); attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
    }
    func testOrdinaryPreviewClosesAndReopens() throws {
        let app = try launch()
        let file = app.buttons.containing(.staticText, identifier: "remote.md").firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 20))
        for _ in 0..<2 {
            file.tap()
            let close = app.buttons["preview.close"]
            XCTAssertTrue(close.waitForExistence(timeout: 15))
            XCTAssertTrue(close.isHittable)
            capture("ordinary-preview", app)
            close.tap()
            XCTAssertTrue(file.waitForExistence(timeout: 5))
            XCTAssertFalse(close.exists)
        }
    }
    func testPreviewNavigatesBetweenTextAndImage() throws {
        let app = try launch()
        let file = app.buttons.containing(.staticText, identifier: "remote.md").firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 20)); file.tap()
        let next = app.buttons["preview.next"], previous = app.buttons["preview.previous"]
        XCTAssertTrue(next.waitForExistence(timeout: 15)); XCTAssertTrue(next.isEnabled)
        XCTAssertFalse(previous.isEnabled)
        next.tap()
        XCTAssertTrue(app.staticTexts["second.png"].waitForExistence(timeout: 15))
        let loaded = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: app.activityIndicators.firstMatch)
        XCTAssertEqual(XCTWaiter.wait(for: [loaded], timeout: 15), .completed)
        XCTAssertFalse(next.isEnabled); XCTAssertTrue(previous.isEnabled)
        XCTAssertTrue(app.buttons["preview.close"].isHittable)
        capture("image-preview-paging", app)
        previous.tap()
        XCTAssertTrue(app.staticTexts["remote.md"].waitForExistence(timeout: 5))
        app.buttons["preview.close"].tap()
        XCTAssertTrue(file.waitForExistence(timeout: 5))
    }
    func testVaultPreviewClosesAndReopens() throws {
        let app = try launch()
        let vault = app.buttons.containing(.staticText, identifier: "vault").firstMatch
        XCTAssertTrue(vault.waitForExistence(timeout: 20)); vault.tap()
        let password = app.secureTextFields.firstMatch
        XCTAssertTrue(password.waitForExistence(timeout: 5)); password.tap(); password.typeText("test-password")
        app.buttons["Unlock"].firstMatch.tap()
        let file = app.buttons.matching(NSPredicate(format: "label ENDSWITH '.txt'")).firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 30))
        for _ in 0..<2 {
            file.tap()
            let close = app.buttons["preview.close"]
            XCTAssertTrue(close.waitForExistence(timeout: 15)); XCTAssertTrue(close.isHittable)
            capture("vault-preview", app)
            close.tap()
            XCTAssertTrue(file.waitForExistence(timeout: 5))
        }
    }
    func testShareLinkAndRevoke() throws {
        let app = try launch()
        let file = app.buttons.containing(.staticText, identifier: "remote.md").firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 20)); file.press(forDuration: 1)
        app.buttons["Share"].firstMatch.tap()
        XCTAssertTrue(app.buttons["Create link"].waitForExistence(timeout: 10))
        app.buttons["Create link"].tap()
        XCTAssertTrue(app.buttons["Copy link"].waitForExistence(timeout: 15))
        capture("sharing", app)
        app.buttons["Revoke access"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["No shared access"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["Copy link"].exists)
    }
    func testLoggingSettingSurvivesRelaunch() throws {
        let app = try launch(settings: true)
        let toggle = app.switches["Save sync logs"]
        for _ in 0..<3 { if toggle.isHittable { break }; app.swipeUp() }
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        if toggle.value as? String == "0" { toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap() }
        XCTAssertTrue(app.buttons["Share log"].waitForExistence(timeout: 5))
        capture("diagnostics", app)
        app.terminate(); app.launch()
        for _ in 0..<3 { if toggle.isHittable { break }; app.swipeUp() }
        XCTAssertEqual(toggle.value as? String, "1")
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.93, dy: 0.5)).tap()
        XCTAssertEqual(toggle.value as? String, "0")
    }
    func testFilesRemoveLocalCopy() throws {
        _ = try launch(settings: true)
        let files = XCUIApplication(bundleIdentifier: "com.apple.DocumentsApp")
        files.launch()
        let previewClose = files.buttons["QLOverlayDoneButtonAccessibilityIdentifier"].firstMatch
        if previewClose.exists && previewClose.isHittable { previewClose.tap() }
        let browse = files.buttons["Browse"].firstMatch
        if browse.waitForExistence(timeout: 10) { browse.tap() }
        for _ in 0..<6 {
            if files.staticTexts["Locations"].exists { break }
            let back = files.buttons["BackButton"]
            if back.isHittable { back.tap() }
            else if files.navigationBars.buttons.firstMatch.exists { files.navigationBars.buttons.firstMatch.tap() }
            else { break }
        }
        let location = files.staticTexts["DiscoDrive"].firstMatch
        XCTAssertTrue(location.waitForExistence(timeout: 15)); location.tap()
        let file = files.staticTexts["remote"].firstMatch
        XCTAssertTrue(file.waitForExistence(timeout: 20))
        // Tap the thumbnail: tapping an already selected filename starts renaming.
        file.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0)).withOffset(CGVector(dx: 0, dy: -60)).tap()
        let done = files.buttons["QLOverlayDoneButtonAccessibilityIdentifier"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 20))
        XCTAssertTrue(files.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", "server note", "server note")).firstMatch.waitForExistence(timeout: 15))
        done.tap()
        XCTAssertTrue(file.waitForExistence(timeout: 10)); file.press(forDuration: 1)
        let remove = files.buttons["Remove local copy"]
        for _ in 0..<3 { if remove.exists { break }; files.swipeUp() }
        XCTAssertTrue(remove.waitForExistence(timeout: 10)); remove.tap()
        XCTAssertTrue(files.staticTexts["Remove the downloaded copy from this device. Unsaved changes must finish syncing first."].waitForExistence(timeout: 10))
        capture("evict-action", files)
        files.buttons["Remove local copy"].firstMatch.tap()
        XCTAssertTrue(files.staticTexts["Local copy removed. The file remains on the server."].waitForExistence(timeout: 15))
        files.buttons["Done"].firstMatch.tap()
    }
}
