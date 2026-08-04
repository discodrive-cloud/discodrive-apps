import Foundation

final class MockURLProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) -> (Int, [String: String], Data))?
    // Body of the last request that reached the protocol. A file or stream upload leaves
    // httpBody nil and hands the payload over as httpBodyStream, so a test that wants to
    // assert on what was actually sent has to drain the stream — reading httpBody alone
    // would silently see nothing and pass.
    nonisolated(unsafe) static var lastBody: Data?

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    // Reads the request payload from whichever of the two places it lives in.
    private static func payload(of request: URLRequest) -> Data {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var out = Data()
        let size = 64 * 1024
        var buf = [UInt8](repeating: 0, count: size)
        while stream.hasBytesAvailable {
            let n = stream.read(&buf, maxLength: size)
            if n <= 0 { break }
            out.append(buf, count: n)
        }
        return out
    }

    // Fails the request at the network layer instead of answering it. Returning nil falls
    // through to `handler`. Lets a test reproduce a connection dropped under it — what an
    // app that is no longer in the foreground sees.
    nonisolated(unsafe) static var failure: ((URLRequest) -> Error?)?

    override func startLoading() {
        MockURLProtocol.lastBody = MockURLProtocol.payload(of: request)
        if let error = MockURLProtocol.failure?(request) {
            client?.urlProtocol(self, didFailWithError: error)
            return
        }
        guard let handler = MockURLProtocol.handler else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse)); return
        }
        let (status, headers, body) = handler(request)
        let resp = HTTPURLResponse(url: request.url!, statusCode: status,
                                   httpVersion: "HTTP/1.1", headerFields: headers)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}

    static func session() -> URLSession {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [MockURLProtocol.self]
        return URLSession(configuration: cfg)
    }
}
