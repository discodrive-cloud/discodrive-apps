import XCTest

/// Settings: the theme picker switches the colour scheme, and the last section offers the
/// App Store review, the privacy policy, the contact address and the version. Needs no
/// pairing: DISCODRIVE_TEST_SCREEN opens Settings directly.
final class SettingsAboutUITests: XCTestCase {
    override func setUp() { continueAfterFailure = false }

    private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchEnvironment["DISCODRIVE_TEST_SCREEN"] = "settings"
        app.launch()
        return app
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name; a.lifetime = .keepAlways; add(a)
    }

    /// The picker's button is labelled with its title and the current choice ("Theme, Dark").
    private func themeButton(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Theme'")).firstMatch
    }

    func testThemePickerAndAboutSection() throws {
        let app = launch()
        let theme = themeButton(app)
        XCTAssertTrue(theme.waitForExistence(timeout: 10))

        theme.tap()
        app.buttons["Dark"].firstMatch.tap()
        shot(app, "settings-dark")

        // The choice survives a relaunch.
        app.terminate()
        let again = launch()
        XCTAssertTrue(themeButton(again).waitForExistence(timeout: 10))
        XCTAssertTrue(themeButton(again).label.contains("Dark"), themeButton(again).label)

        // Scrolled by the button: text in a cell does not count as hittable.
        let rate = again.buttons["Rate DiscoDrive"]
        for _ in 0..<6 where !rate.isHittable { again.swipeUp() }
        XCTAssertTrue(rate.isHittable)
        // The row reads as "Version, 0.1.6 (1006)": the marketing version and the build number.
        let version = again.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Version,'")).firstMatch
        XCTAssertTrue(version.exists)
        XCTAssertNotNil(version.label.range(of: #"\d+\.\d+(\.\d+)?\S* \(\d+\)$"#, options: .regularExpression), version.label)
        XCTAssertTrue(again.buttons["Privacy policy"].exists)
        XCTAssertTrue(again.buttons.matching(NSPredicate(format: "label CONTAINS 'Contact us'")).firstMatch.exists)
        shot(again, "settings-about-dark")

        // Back to the default, so other runs start from the system theme.
        for _ in 0..<6 where !themeButton(again).isHittable { again.swipeDown() }
        themeButton(again).tap()
        again.buttons["System default"].firstMatch.tap()
        XCTAssertTrue(themeButton(again).label.contains("System default"))
    }
}
