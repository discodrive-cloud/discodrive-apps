import Foundation

/// How long the app has been in use, for deciding when to ask for a review. Days are days
/// since 1970 in the user's calendar.
public struct ReviewState: Equatable, Sendable {
    public var firstDay: Int?
    public var lastDay: Int?
    public var activeDays: Int
    public var askedDay: Int?

    public init(firstDay: Int? = nil, lastDay: Int? = nil, activeDays: Int = 0, askedDay: Int? = nil) {
        self.firstDay = firstDay; self.lastDay = lastDay; self.activeDays = activeDays; self.askedDay = askedDay
    }
}

/// Asks for a review only from someone who has actually been using the app: on several
/// different days, spread over at least a week. After asking, it waits months before asking
/// again. The store has its own limit on top (the App Store: three times a year) and may show
/// nothing at all. The same rule as the Android client's ReviewPolicy.
public enum ReviewPolicy {
    public static let minActiveDays = 4
    public static let minDaysSinceFirst = 7
    public static let daysBetweenAsks = 120

    public static func shouldAsk(_ s: ReviewState, today: Int) -> Bool {
        guard let first = s.firstDay, s.activeDays >= minActiveDays, today - first >= minDaysSinceFirst else {
            return false
        }
        guard let asked = s.askedDay else { return true }
        return today - asked >= daysBetweenAsks
    }

    /// Today as a day number for [ReviewState].
    public static func today(_ now: Date = Date(), calendar: Calendar = .current) -> Int {
        let start = calendar.startOfDay(for: now)
        return Int((start.timeIntervalSince1970 + Double(calendar.timeZone.secondsFromGMT(for: start))) / 86_400)
    }
}

/// Keeps [ReviewState] in UserDefaults. Not part of the pairing: it describes the person
/// using the phone, so signing out leaves it alone.
public final class ReviewTracker: @unchecked Sendable {
    private let defaults: UserDefaults
    private enum Key {
        static let first = "review.firstDay", last = "review.lastDay"
        static let days = "review.activeDays", asked = "review.askedDay"
    }

    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    public var state: ReviewState {
        ReviewState(
            firstDay: defaults.object(forKey: Key.first) as? Int,
            lastDay: defaults.object(forKey: Key.last) as? Int,
            activeDays: defaults.integer(forKey: Key.days),
            askedDay: defaults.object(forKey: Key.asked) as? Int)
    }

    /// Counts [day] as a day the app was used; the same day twice counts once.
    public func recordActiveDay(_ day: Int) {
        let s = state
        guard s.lastDay != day else { return }
        defaults.set(s.firstDay ?? day, forKey: Key.first)
        defaults.set(day, forKey: Key.last)
        defaults.set(s.activeDays + 1, forKey: Key.days)
    }

    public func markAsked(_ day: Int) { defaults.set(day, forKey: Key.asked) }

    /// Records today and says whether to ask now. A yes is counted as asked straight away:
    /// the store does not report whether it showed anything.
    public func recordAndDecide(today: Int = ReviewPolicy.today()) -> Bool {
        recordActiveDay(today)
        guard ReviewPolicy.shouldAsk(state, today: today) else { return false }
        markAsked(today)
        return true
    }
}
