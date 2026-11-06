import SafariServices
import SwiftUI

/// Shows a web page inside the app with Safari View Controller, so the user signs in
/// without leaving it and can still check the page's address and certificate.
struct SafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController {
        let config = SFSafariViewController.Configuration()
        config.entersReaderIfAvailable = false
        let vc = SFSafariViewController(url: url, configuration: config)
        vc.dismissButtonStyle = .done
        return vc
    }

    func updateUIViewController(_ vc: SFSafariViewController, context: Context) {}
}

/// A URL that SwiftUI's `sheet(item:)` can present.
struct PresentedURL: Identifiable, Equatable {
    let url: URL
    var id: String { url.absoluteString }
}
