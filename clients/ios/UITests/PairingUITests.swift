import XCTest

/// App Store guideline 4: the server's sign-in page must open inside the app, not in the
/// default browser. Needs an unpaired install and a server with a publicly trusted
/// certificate (DD_PAIR_SERVER, e.g. https://demo.discodrive.app); nothing is confirmed.
@MainActor
final class PairingUITests: XCTestCase {
    func testSignInPageOpensInsideTheApp() throws {
        continueAfterFailure = false
        let server = ProcessInfo.processInfo.environment["DD_PAIR_SERVER"]
        try XCTSkipIf(server == nil, "Needs DD_PAIR_SERVER")
        let app = XCUIApplication()
        app.launch()

        let field = app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "pairing screen expected (unpaired install)")
        field.tap()
        field.press(forDuration: 1.0)
        if app.menuItems["Select All"].waitForExistence(timeout: 2) { app.menuItems["Select All"].tap() }
        field.typeText(server! + "\n")

        // Safari View Controller runs in the app's own window: its web view and Done button
        // belong to this app, while Safari would have sent the app to the background.
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 20), "in-app sign-in page expected")
        XCTAssertEqual(app.state, .runningForeground)
        // The page renders in Safari's own process, outside the app's element tree: give it
        // time, then capture the whole screen rather than the app's window.
        Thread.sleep(forTimeInterval: 8)
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "sign-in-in-app"; shot.lifetime = .keepAlways; add(shot)

        // Closing the page keeps the pairing waiting and offers to reopen it.
        app.buttons["Done"].tap()
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS[c] 'sign-in' OR label CONTAINS[c] 'входа'")).firstMatch
            .waitForExistence(timeout: 5), "reopen button expected")
        let after = XCTAttachment(screenshot: app.screenshot())
        after.name = "after-close"; after.lifetime = .keepAlways; add(after)
    }
}
