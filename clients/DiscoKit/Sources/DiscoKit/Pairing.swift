import Foundation

public struct PairingInfo: Sendable {
    public let deviceCode: String
    public let userCode: String
    public let verificationURI: String
    public let interval: Int
}

public struct Pairing: Sendable {
    let baseURL: URL
    let session: URLSession
    public init(baseURL: URL, session: URLSession = DiscoNet.session) {
        self.baseURL = baseURL; self.session = session
    }

    public func start(deviceName: String) async throws -> PairingInfo {
        var req = URLRequest(url: baseURL.appendingPathComponent("pair/init"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(["name": deviceName, "kind": "desktop"])
        let (data, resp) = try await session.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 201 else { throw APIError.badResponse }
        struct Out: Decodable {
            let device_code: String; let user_code: String
            let verification_uri: String; let interval: Int
        }
        let o = try JSONDecoder().decode(Out.self, from: data)
        return PairingInfo(deviceCode: o.device_code, userCode: o.user_code,
                           verificationURI: o.verification_uri, interval: o.interval)
    }

    /// Waits for the user to approve the code, returning the device token.
    ///
    /// `networkGrace` is how long polling continues while every request fails at the network
    /// layer. Approving means leaving for a browser, so the app stops being in the foreground
    /// and loses its connections — treating the first such failure as fatal ended the very
    /// pairing the user had gone off to approve. Any answered request resets the window, so
    /// only an unbroken run of failures — a server that is genuinely unreachable rather than
    /// an app that was backgrounded — gives up.
    public func poll(deviceCode: String, interval: Duration,
                     networkGrace: Duration = .seconds(120)) async throws -> String {
        struct Out: Decodable { let status: String; let device_token: String? }
        let clock = ContinuousClock()
        var failingSince: ContinuousClock.Instant?
        while true {
            var req = URLRequest(url: baseURL.appendingPathComponent("pair/token"))
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONEncoder().encode(["device_code": deviceCode])
            let data: Data
            do {
                data = try await session.data(for: req).0
            } catch let error as URLError {
                let since = failingSince ?? clock.now
                failingSince = since
                if clock.now - since >= networkGrace { throw error }
                try await Task.sleep(for: interval)
                continue
            }
            failingSince = nil
            let o = try JSONDecoder().decode(Out.self, from: data)
            switch o.status {
            case "approved": return o.device_token ?? ""
            case "pending": try await Task.sleep(for: interval)
            default: throw APIError.notAuthenticated
            }
        }
    }
}
