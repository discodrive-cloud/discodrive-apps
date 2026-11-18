import SwiftUI
import StoreKit
import DiscoKit

/// The app's colour scheme: the phone's setting, or always light, or always dark. Stored in
/// UserDefaults under [storageKey], not with the pairing: signing out keeps it.
enum ThemeMode: String, CaseIterable, Identifiable {
    case system, light, dark
    static let storageKey = "ui.themeMode"
    var id: String { rawValue }
    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
    var titleKey: String { "theme." + rawValue }
}

/// Where Settings sends people: the App Store review form, the privacy policy, the address
/// for questions. The landing has English and Russian pages only.
enum AppLinks {
    static let appStoreID = "6821574934"
    static let writeReview = URL(string: "https://apps.apple.com/app/id\(appStoreID)?action=write-review")!
    static let contactEmail = "info@discodrive.app"
    static func privacy(language: String) -> URL {
        URL(string: "https://discodrive.app/" + (language == "ru" ? "ru/" : "") + "privacy")!
    }
    static var contact: URL { URL(string: "mailto:\(contactEmail)")! }

    /// "0.1.6 (1006)": the marketing version and the build, as App Store Connect shows them.
    static var version: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "?"
        let build = info?["CFBundleVersion"] as? String ?? "?"
        return "\(short) (\(build))"
    }
}

/// Asks for an App Store review once [ReviewPolicy] says the app has been in real use. Applied
/// to the paired file browser; runs when it appears and whenever the app comes back to the
/// foreground. The Settings button opens the review form instead: Apple asks not to tie the
/// system prompt to a button, since it may show nothing.
struct ReviewPrompt: ViewModifier {
    @Environment(\.requestReview) private var requestReview
    @Environment(\.scenePhase) private var scenePhase
    private let tracker = ReviewTracker()

    func body(content: Content) -> some View {
        content
            .task { await askIfDue() }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active { Task { await askIfDue() } }
            }
    }

    @MainActor private func askIfDue() async {
        guard tracker.recordAndDecide() else { return }
        // Not over the browser as it appears: give the screen a moment first.
        try? await Task.sleep(for: .seconds(2))
        requestReview()
    }
}

extension View {
    func reviewPrompt() -> some View { modifier(ReviewPrompt()) }
}
