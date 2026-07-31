import XCTest
@testable import DiscoKit

final class UploadFileTests: XCTestCase {

    private func fixture(_ bytes: Int, byte: UInt8 = 0x41) throws -> (URL, Data) {
        let payload = Data(repeating: byte, count: bytes)
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("ddk-\(UUID().uuidString).bin")
        try payload.write(to: url)
        addTeardownBlock { try? FileManager.default.removeItem(at: url) }
        return (url, payload)
    }

    // The file has to arrive byte for byte, streamed rather than pulled through memory.
    // 4 MB is well past any single read buffer, so a truncated stream would show up here.
    func testUploadFromFileSendsExactBytes() async throws {
        let (url, payload) = try fixture(4 * 1024 * 1024)
        MockURLProtocol.lastBody = nil
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"T"}"#.utf8))
            }
            return (201, [:], Data("{}".utf8))
        }
        let c = APIClient(baseURL: URL(string: "https://x.test")!,
                          deviceToken: "D", session: MockURLProtocol.session())

        try await c.uploadFile(relPath: "/dir/big.bin", fileURL: url)

        XCTAssertEqual(MockURLProtocol.lastBody?.count, payload.count,
                       "streamed body has the wrong length")
        XCTAssertEqual(MockURLProtocol.lastBody, payload,
                       "streamed body differs from the file on disk")
    }

    // The content date has to reach the server, or a photo from 2019 lands dated today.
    func testUploadFromFileSendsModifiedAt() async throws {
        let (url, _) = try fixture(16)
        var seen: String?
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"T"}"#.utf8))
            }
            seen = req.value(forHTTPHeaderField: "X-Modified-At")
            return (201, [:], Data("{}".utf8))
        }
        let c = APIClient(baseURL: URL(string: "https://x.test")!,
                          deviceToken: "D", session: MockURLProtocol.session())

        let want = Date(timeIntervalSince1970: 1_560_602_400) // 2019-06-15T12:00:00Z
        try await c.uploadFile(relPath: "/a.bin", fileURL: url, modifiedAt: want)

        let header = try XCTUnwrap(seen, "X-Modified-At was not sent")
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let parsed = try XCTUnwrap(f.date(from: header), "X-Modified-At is not RFC3339: \(header)")
        XCTAssertEqual(parsed.timeIntervalSince1970, want.timeIntervalSince1970, accuracy: 0.001)
    }

    // No date to declare means no header at all, leaving the server's own dating alone.
    func testUploadFromFileOmitsMissingModifiedAt() async throws {
        let (url, _) = try fixture(16)
        var sawHeader = true
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"T"}"#.utf8))
            }
            sawHeader = req.value(forHTTPHeaderField: "X-Modified-At") != nil
            return (201, [:], Data("{}".utf8))
        }
        let c = APIClient(baseURL: URL(string: "https://x.test")!,
                          deviceToken: "D", session: MockURLProtocol.session())

        try await c.uploadFile(relPath: "/a.bin", fileURL: url)
        XCTAssertFalse(sawHeader, "X-Modified-At was sent without a date")
    }

    // The 401 retry re-reads the file, so the second attempt carries the whole body — a
    // partial resend would store a truncated file the server accepts as complete.
    func testUploadFromFileResendsWholeBodyAfterUnauthorized() async throws {
        let (url, payload) = try fixture(512 * 1024)
        var uploads = 0
        MockURLProtocol.lastBody = nil
        MockURLProtocol.handler = { req in
            if req.url!.path.hasSuffix("auth/device/token") {
                return (200, [:], Data(#"{"token":"T"}"#.utf8))
            }
            uploads += 1
            return uploads == 1 ? (401, [:], Data()) : (201, [:], Data("{}".utf8))
        }
        let c = APIClient(baseURL: URL(string: "https://x.test")!,
                          deviceToken: "D", session: MockURLProtocol.session())

        try await c.uploadFile(relPath: "/retry.bin", fileURL: url)

        XCTAssertEqual(uploads, 2, "the 401 was not retried")
        XCTAssertEqual(MockURLProtocol.lastBody, payload,
                       "the retry did not re-send the whole file")
    }

    // NOTE: "an unreadable file must fail loudly" is deliberately not tested here.
    // MockURLProtocol answers the request before URLSession ever opens the file, so the
    // mock would report success no matter what; and against a real session the connection
    // error arrives first (-1004), never the file error. What the fix actually removed is
    // the `try? Data(contentsOf:) else { continue }` swallow at the AppState call sites —
    // and AppState lives outside this package, so it is not covered by these tests.

    // contentModificationDate is what the call sites feed into modifiedAt.
    func testContentModificationDateReadsTheFile() throws {
        let (url, _) = try fixture(8)
        let want = Date(timeIntervalSince1970: 1_600_000_000)
        try FileManager.default.setAttributes([.modificationDate: want], ofItemAtPath: url.path)

        let got = try XCTUnwrap(APIClient.contentModificationDate(of: url))
        XCTAssertEqual(got.timeIntervalSince1970, want.timeIntervalSince1970, accuracy: 1)
    }
}
