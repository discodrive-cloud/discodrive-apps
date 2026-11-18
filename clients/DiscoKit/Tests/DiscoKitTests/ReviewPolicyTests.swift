import XCTest
@testable import DiscoKit

final class ReviewPolicyTests: XCTestCase {
    private func state(first: Int, days: Int, asked: Int? = nil) -> ReviewState {
        ReviewState(firstDay: first, lastDay: first + days, activeDays: days, askedDay: asked)
    }

    func testNeverOnAFreshInstall() {
        XCTAssertFalse(ReviewPolicy.shouldAsk(ReviewState(), today: 0))
        XCTAssertFalse(ReviewPolicy.shouldAsk(state(first: 100, days: 1), today: 100))
    }

    func testNotBeforeAWeekEvenWithManyActiveDays() {
        XCTAssertFalse(ReviewPolicy.shouldAsk(state(first: 100, days: 6), today: 106))
    }

    func testNotWhenUsedOnTooFewDays() {
        XCTAssertFalse(ReviewPolicy.shouldAsk(state(first: 100, days: 3), today: 130))
    }

    func testAsksAfterAWeekOfRegularUse() {
        XCTAssertTrue(ReviewPolicy.shouldAsk(state(first: 100, days: 4), today: 107))
    }

    func testAsksAgainOnlyAfterALongPause() {
        XCTAssertFalse(ReviewPolicy.shouldAsk(state(first: 100, days: 10, asked: 110), today: 200))
        XCTAssertTrue(ReviewPolicy.shouldAsk(state(first: 100, days: 10, asked: 110), today: 230))
    }
}

final class ReviewTrackerTests: XCTestCase {
    private var defaults: UserDefaults!
    private let suite = "ReviewTrackerTests"

    override func setUp() {
        UserDefaults().removePersistentDomain(forName: suite)
        defaults = UserDefaults(suiteName: suite)
    }
    override func tearDown() { UserDefaults().removePersistentDomain(forName: suite) }

    func testActiveDaysCountEachDayOnce() {
        let t = ReviewTracker(defaults: defaults)
        t.recordActiveDay(100); t.recordActiveDay(100); t.recordActiveDay(101)
        XCTAssertEqual(t.state, ReviewState(firstDay: 100, lastDay: 101, activeDays: 2, askedDay: nil))
    }

    func testAskingIsRemembered() {
        let t = ReviewTracker(defaults: defaults)
        t.recordActiveDay(100)
        t.markAsked(110)
        XCTAssertEqual(ReviewTracker(defaults: defaults).state.askedDay, 110)
    }

    func testRecordAndDecideAsksOnceThenWaits() {
        let t = ReviewTracker(defaults: defaults)
        for day in [100, 102, 104, 106] { XCTAssertFalse(t.recordAndDecide(today: day)) }
        XCTAssertTrue(t.recordAndDecide(today: 107))
        XCTAssertFalse(t.recordAndDecide(today: 108))
    }

    func testNewSettingsStringsExistInEveryLanguage() {
        for key in ["settings.theme", "theme.system", "theme.light", "theme.dark",
                    "settings.rate", "settings.privacy", "settings.contact", "settings.version"] {
            for lang in L10n.supported {
                XCTAssertFalse(L10n.table[key]?[lang]?.isEmpty ?? true, "\(key) missing in \(lang)")
            }
        }
    }
}
