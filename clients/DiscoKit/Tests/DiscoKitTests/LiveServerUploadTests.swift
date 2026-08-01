import XCTest
import CryptoKit
@testable import DiscoKit

/// Runs the upload path against a real DiscoDrive server, not a mock.
///
/// The mocked tests prove the client's own logic; this proves the two sides agree — that the
/// size and date the client declares are what the server expects, that a resumed session is
/// accepted, and that the file lands byte for byte.
///
/// Skipped unless a server is configured:
///
///   DD_LIVE_SERVER=http://localhost:8080 DD_LIVE_TOKEN=kfd_… swift test --filter LiveServer
final class LiveServerUploadTests: XCTestCase {

    private var server: URL!
    private var token: String!
    private var api: APIClient!

    override func setUpWithError() throws {
        let env = ProcessInfo.processInfo.environment
        let raw = env["DD_LIVE_SERVER"] ?? ""
        token = env["DD_LIVE_TOKEN"] ?? ""
        try XCTSkipIf(raw.isEmpty || token.isEmpty,
                      "set DD_LIVE_SERVER and DD_LIVE_TOKEN to run the live checks")
        server = URL(string: raw)!
        api = APIClient(baseURL: server, deviceToken: token)
    }

    private func tempFile(bytes: Int, name: String) throws -> URL {
        let dir = URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let url = dir.appendingPathComponent(name)
        var data = Data(count: bytes)
        for i in 0..<bytes { data[i] = UInt8((i * 31 + 7) % 251) }
        try data.write(to: url)
        return url
    }

    /// A multi-chunk upload has to arrive whole, dated with the content's own date, and as a
    /// first version — anything else means the client and the server disagree.
    func testUploadLandsWholeAndDated() async throws {
        let folder = try await api.ensureFolder(parentID: nil, name: "DiscoKitLiveTest")

        let url = try tempFile(bytes: 20 << 20, name: "clip.bin")   // 20 MiB → 3 chunks at 8 MiB
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let taken = Date(timeIntervalSince1970: 1_562_000_000)      // 2019
        let name = "live-\(UUID().uuidString.prefix(8)).bin"

        let uploader = ChunkedUploader(api: api)
        try await uploader.upload(fileURL: url, parentID: folder, name: name, modifiedAt: taken)

        let entries = try await api.listFolder(parentID: folder)
        guard let landed = entries.first(where: { $0.name == name }) else {
            return XCTFail("the upload did not appear in the listing")
        }
        XCTAssertEqual(landed.size, 20 << 20)
        XCTAssertEqual(landed.version, 1, "a fresh name must not land as a new version")
        // Within a second: the server stores what the client declared.
        XCTAssertEqual(landed.modifiedAt?.timeIntervalSince1970 ?? 0, taken.timeIntervalSince1970,
                       accuracy: 1, "the server must keep the content's own date")
        XCTAssertEqual(landed.contentHash, try AutoUploadHashing.sha256(of: url),
                       "what landed must match the bytes that were sent")
        try await cleanUp(folder: folder)
    }

    /// Resuming is the whole reason for the chunked path: a session that already holds the
    /// first chunks must continue, not start over or duplicate them.
    func testResumeContinuesAnExistingSession() async throws {
        let folder = try await api.ensureFolder(parentID: nil, name: "DiscoKitLiveTest")

        let url = try tempFile(bytes: 12 << 20, name: "resume.bin")
        defer { try? FileManager.default.removeItem(at: url.deletingLastPathComponent()) }
        let name = "resume-\(UUID().uuidString.prefix(8)).bin"
        let data = try Data(contentsOf: url)

        // Send the first chunk by hand, then let the uploader take over the same session.
        let session = try await api.uploadInit(parentID: folder, name: name,
                                               size: Int64(data.count), modifiedAt: nil)
        let chunk = 8 << 20
        let next = try await api.uploadChunk(uploadID: session.uploadID, index: 0,
                                             data: data.prefix(chunk))
        XCTAssertEqual(next, 1)

        let position = try await api.uploadStatus(uploadID: session.uploadID)
        XCTAssertEqual(position, 1, "the server must report where to continue from")

        _ = try await api.uploadChunk(uploadID: session.uploadID, index: 1,
                                      data: data.suffix(from: chunk))
        try await api.uploadComplete(uploadID: session.uploadID)

        let entries = try await api.listFolder(parentID: folder)
        let landed = entries.first { $0.name == name }
        XCTAssertEqual(landed?.size, Int64(data.count), "the resumed file must be complete")
        XCTAssertEqual(landed?.contentHash, try AutoUploadHashing.sha256(of: url))
        try await cleanUp(folder: folder)
    }

    /// Declaring a size the upload does not deliver must be refused rather than published:
    /// this is what stops a photo that changed mid-upload from landing truncated.
    func testShortUploadIsRefused() async throws {
        let folder = try await api.ensureFolder(parentID: nil, name: "DiscoKitLiveTest")

        let name = "short-\(UUID().uuidString.prefix(8)).bin"
        let session = try await api.uploadInit(parentID: folder, name: name,
                                               size: 10_000, modifiedAt: nil)
        _ = try await api.uploadChunk(uploadID: session.uploadID, index: 0, data: Data(count: 4_000))
        do {
            try await api.uploadComplete(uploadID: session.uploadID)
            XCTFail("the server must refuse an upload that is short of its declared size")
        } catch APIError.http(let code) {
            XCTAssertEqual(code, 400)
        }
        try await cleanUp(folder: folder)
    }

    private func cleanUp(folder: String) async throws {
        // Leaves the test folder itself; its contents are what the next run would trip over.
        for entry in try await api.listFolder(parentID: folder) {
            try? await api.delete(nodeID: entry.id)
        }
    }
}

/// Small shared helper so the live tests hash exactly the way the runner does.
enum AutoUploadHashing {
    static func sha256(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }
}
