import XCTest
@testable import DiscoKit

final class RedirectTests: XCTestCase {
    func testEveryAccountSessionEnforcesRedirectPolicy() {
        for pin in [nil, CertificatePinningTests.selfSignedFingerprint] as [String?] {
            let session = DiscoNet.session(pin: pin)
            guard let delegate = session.delegate as? URLSessionTaskDelegate else {
                XCTFail("session has no redirect policy"); continue
            }
            let original = URL(string: "https://server.test/auth/device/token")!
            let task = session.dataTask(with: original)
            defer { task.cancel() }
            for target in ["https://server.test/next", "https://SERVER.test:443/next",
                           "http://server.test/next", "https://other.test/next",
                           "https://server.test:444/next", "https://user:pass@server.test/next"] {
                let url = URL(string: target)!
                let response = HTTPURLResponse(url: original, statusCode: 307, httpVersion: nil, headerFields: ["Location": target])!
                let result = expectation(description: target)
                delegate.urlSession?(session, task: task, willPerformHTTPRedirection: response,
                                     newRequest: URLRequest(url: url)) { accepted in
                    let safe = target == "https://server.test/next" || target == "https://SERVER.test:443/next"
                    XCTAssertEqual(accepted != nil, safe, target)
                    result.fulfill()
                }
                wait(for: [result], timeout: 1)
            }
        }
    }
}
