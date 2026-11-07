import UIKit
import UniformTypeIdentifiers
import DiscoKit

// "Share → DiscoDrive": takes the link, asks the server once (≤ 1.5 s), shows one line for
// about a second and closes. No fields, no buttons. A link that could not get through in
// time waits in the App Group outbox; the app and the next share send it.
final class ShareViewController: UIViewController {
    private let label = UILabel()
    private var finished = false
    // Set when the network works: what earlier shares left behind goes out while the line
    // is on screen, and whatever is not sent by then waits for the app.
    private var backlog: (LinkOutbox, APIClient)?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .clear
        let hud = UIVisualEffectView(effect: UIBlurEffect(style: .systemThickMaterialDark))
        hud.layer.cornerRadius = 16
        hud.clipsToBounds = true
        hud.translatesAutoresizingMaskIntoConstraints = false
        label.textColor = .white
        label.font = .preferredFont(forTextStyle: .headline)
        label.textAlignment = .center
        label.numberOfLines = 2
        label.translatesAutoresizingMaskIntoConstraints = false
        hud.contentView.addSubview(label)
        view.addSubview(hud)
        NSLayoutConstraint.activate([
            hud.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            hud.centerYAnchor.constraint(equalTo: view.centerYAnchor),
            hud.widthAnchor.constraint(lessThanOrEqualTo: view.widthAnchor, constant: -48),
            label.topAnchor.constraint(equalTo: hud.contentView.topAnchor, constant: 18),
            label.bottomAnchor.constraint(equalTo: hud.contentView.bottomAnchor, constant: -18),
            label.leadingAnchor.constraint(equalTo: hud.contentView.leadingAnchor, constant: 24),
            label.trailingAnchor.constraint(equalTo: hud.contentView.trailingAnchor, constant: -24),
        ])
        hud.isHidden = true
        Task { await run(hud: hud) }
    }

    private var language: String {
        let group = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveAppGroup") as? String
        if let group, let lang = UserDefaults(suiteName: group)?.string(forKey: "ui_language") { return lang }
        return Locale.preferredLanguages.first ?? "en"
    }

    private func run(hud: UIView) async {
        let outcome = await save()
        label.text = LinkShare.message(for: outcome, language: language)
        hud.isHidden = false
        let flush = backlog.map { box, client in Task { await LinkShare.flush(outbox: box, client: client) } }
        try? await Task.sleep(for: .seconds(outcome == .saved || outcome == .queued ? 1 : 1.8))
        flush?.cancel()
        finish()
    }

    private func finish() {
        guard !finished else { return }
        finished = true
        extensionContext?.completeRequest(returningItems: nil)
    }

    private func save() async -> LinkShare.Outcome {
        guard let (url, title) = await sharedLink() else { return .rejected }
        guard let client = makeClient() else { return .signIn }
        let group = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveAppGroup") as? String
        let outbox = group.flatMap { LinkOutbox(appGroup: $0) }

        let status: Int? = await withTaskGroup(of: Int?.self) { tasks in
            tasks.addTask {
                do { return try await client.saveLink(url: url, title: title) }
                catch APIError.notAuthenticated { return 401 }
                catch { return nil }
            }
            tasks.addTask {
                try? await Task.sleep(for: LinkShare.answerTimeout)
                return nil
            }
            let first = await tasks.next() ?? nil
            tasks.cancelAll()
            return first
        }
        let outcome = LinkShare.outcome(status: status)
        switch outcome {
        case .queued:
            // A request still in flight may land as well: the server keeps one row per link.
            outbox?.enqueue(url, title: title)
        case .saved:
            if let outbox { backlog = (outbox, client) }
        case .signIn, .rejected:
            break
        }
        return outcome
    }

    // The pairing the app stored in the shared Keychain; nil when this device is not paired.
    private func makeClient() -> APIClient? {
        KeychainConfig.accessGroup = Bundle.main.object(forInfoDictionaryKey: "DiscoDriveKeychainGroup") as? String
        guard let token = try? KeychainToken.loadShared(service: KeychainToken.tokenService),
              let server = try? KeychainToken.loadShared(service: KeychainToken.serverService),
              let url = URL(string: server), URLPolicy.isAllowedServer(url) else { return nil }
        DiscoNet.pin = try? KeychainToken.loadShared(service: KeychainToken.pinService)
        return APIClient(baseURL: url, deviceToken: token)
    }

    // The shared link and, when the app shared text alongside it, that text as the title.
    private func sharedLink() async -> (URL, String?)? {
        let items = extensionContext?.inputItems.compactMap { $0 as? NSExtensionItem } ?? []
        var text = items.compactMap { $0.attributedContentText?.string }.first { !$0.isEmpty }
        var found: URL?
        for provider in items.flatMap({ $0.attachments ?? [] }) {
            if found == nil, provider.hasItemConformingToTypeIdentifier(UTType.url.identifier),
               let item = try? await provider.loadItem(forTypeIdentifier: UTType.url.identifier),
               let url = (item as? URL) ?? (item as? NSURL).map({ $0 as URL }),
               let scheme = url.scheme?.lowercased(), scheme == "http" || scheme == "https" {
                found = url
            }
            if text == nil, provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier),
               let item = try? await provider.loadItem(forTypeIdentifier: UTType.plainText.identifier) {
                text = item as? String
            }
        }
        if found == nil, let text { found = LinkShare.firstWebURL(in: text) }
        guard let url = found else { return nil }
        return (url, Self.title(from: text, url: url))
    }

    // Shared text minus the link itself; nil when nothing else is left.
    static func title(from text: String?, url: URL) -> String? {
        guard var t = text else { return nil }
        t = t.replacingOccurrences(of: url.absoluteString, with: "")
        if let other = LinkShare.firstWebURL(in: t) { t = t.replacingOccurrences(of: other.absoluteString, with: "") }
        t = t.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : String(t.prefix(300))
    }
}
