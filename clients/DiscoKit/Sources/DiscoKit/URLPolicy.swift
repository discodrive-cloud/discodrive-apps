import Foundation

/// Which server-supplied links the app may hand to the system.
///
/// Opening a URL goes to whatever handler owns its scheme — `smb://` makes Finder ask for
/// credentials, `x-apple.systempreferences:` opens a settings pane — so a link the server
/// sends (a pairing's verification_uri) is opened only when it is a web page on the very
/// server the user typed in. Anything else is shown as text for the user to copy.
public enum URLPolicy {
    /// Whether the app may talk to a server at this address: https anywhere, plain http only
    /// to this machine (development). Mirrors the Go core's CheckServerURL. The apps turn
    /// ATS off for self-signed servers, so this is what keeps the device token and files
    /// from crossing a network in clear text.
    public static func isAllowedServer(_ url: URL) -> Bool {
        guard let host = url.host?.lowercased(), !host.isEmpty, url.user == nil, url.password == nil else { return false }
        switch url.scheme?.lowercased() {
        case "https": return true
        case "http": return ["localhost", "127.0.0.1", "::1", "[::1]"].contains(host)
        default: return false
        }
    }

    public static func isOpenable(_ url: URL, relativeTo server: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(), scheme == "https" || scheme == "http",
              let serverScheme = server.scheme?.lowercased(),
              let host = url.host?.lowercased(), !host.isEmpty,
              let serverHost = server.host?.lowercased(), !serverHost.isEmpty else { return false }
        // Same origin: scheme, host and port all match the server's. An https server's page
        // is never followed over http, and an http server's https side (a different port)
        // is shown as text like any other link.
        guard scheme == serverScheme else { return false }
        return host == serverHost && port(of: url, scheme: scheme) == port(of: server, scheme: serverScheme)
    }

    // The port a URL reaches, its scheme's default when none is written.
    private static func port(of url: URL, scheme: String) -> Int? {
        url.port ?? (scheme == "https" ? 443 : scheme == "http" ? 80 : nil)
    }
}

/// A server address that `URLPolicy.isAllowedServer` refuses.
public struct InsecureServerURLError: LocalizedError, Equatable, Sendable {
    public init() {}
    // Same text as the Go core's ErrInsecureServerURL.
    public var errorDescription: String? { "server URL must use https" }
}
