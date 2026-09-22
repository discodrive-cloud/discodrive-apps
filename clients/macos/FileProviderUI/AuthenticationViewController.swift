import AppKit
import FileProviderUI
import DiscoKit

final class AuthenticationViewController: FPUIActionExtensionViewController {
    private var authenticationRequested = false
    private var opening = false
    private let message = NSTextField(wrappingLabelWithString: "")
    private func text(_ key: String) -> String {
        let language = Locale.preferredLanguages.first?.components(separatedBy: "-").first ?? "en"
        return L10n.t(key, language)
    }

    override func loadView() {
        view = NSView(frame: NSRect(x: 0, y: 0, width: 420, height: 150))
        message.stringValue = text("finder.signIn")
        let button = NSButton(title: text("finder.openApp"), target: self, action: #selector(openApp))
        let stack = NSStackView(views: [message, button])
        stack.orientation = .vertical
        stack.spacing = 20
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 24),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -24),
            stack.centerYAnchor.constraint(equalTo: view.centerYAnchor),
        ])
    }

    override func prepare(forError error: Error) {
        authenticationRequested = true
        if isViewLoaded, view.window != nil { openApp() }
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        if authenticationRequested { openApp() }
    }

    @objc private func openApp() {
        guard !opening else { return }
        opening = true
        var url = URLComponents()
        url.scheme = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveURLScheme") as? String ?? "discodrive"
        url.host = "authenticate"
        if let domain = extensionContext.domainIdentifier {
            url.queryItems = [URLQueryItem(name: "domain", value: domain.rawValue)]
        }
        // Use this extension's containing app, not an arbitrary registered Debug copy.
        let appURL = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        NSWorkspace.shared.open([url.url!], withApplicationAt: appURL,
                                configuration: NSWorkspace.OpenConfiguration()) { [weak self] _, error in
            Task { @MainActor in
                guard let self else { return }
                self.opening = false
                if error == nil { self.extensionContext.completeRequest() }
                else { self.message.stringValue = self.text("finder.openFailed") }
            }
        }
    }
}
