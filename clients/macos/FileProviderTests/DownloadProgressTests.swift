import XCTest
import FileProvider
import CryptoKit
import os
import Network
import DiscoKit

final class DownloadProgressTests: XCTestCase {
    func testSuccessfulDownloadCompletesFinderProgress() async throws {
        try await checkDownload(status: 200)
    }

    func testFailedDownloadDoesNotCompleteFinderProgress() async throws {
        try await checkDownload(status: 500)
    }

    func testBytesAreReportedBeforeTheDownloadIsPublished() async throws {
        let destination = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        addTeardownBlock { try? FileManager.default.removeItem(at: destination); MockURLProtocol.handler = nil }
        let bytes = Data(repeating: 42, count: 256 * 1024)
        // URLProtocol does not emit URLSessionDownloadDelegate byte callbacks.
        // Exercise the real HTTP transport with a deliberately chunked local server.
        let listener = try NWListener(using: .tcp, on: .any)
        let ready = expectation(description: "local server ready")
        listener.stateUpdateHandler = { state in if case .ready = state { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            connection.start(queue: .global())
            connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { request, _, _, _ in
                let auth = request.map { String(decoding: $0, as: UTF8.self).hasPrefix("POST ") } ?? false
                let body = auth ? Data(#"{"token":"test"}"#.utf8) : bytes
                let header = Data("HTTP/1.1 200 OK\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n".utf8)
                connection.send(content: header, completion: .contentProcessed { _ in
                    Self.send(body, offset: 0, on: connection)
                })
            }
        }
        listener.start(queue: .global())
        defer { listener.cancel() }
        await fulfillment(of: [ready], timeout: 5)
        let port = try XCTUnwrap(listener.port).rawValue
        let sawNetworkProgress = OSAllocatedUnfairLock(initialState: false)
        let client = APIClient(baseURL: URL(string: "http://127.0.0.1:\(port)")!, deviceToken: "test", session: URLSession(configuration: .ephemeral))
        try await client.download(nodeID: "file", to: destination, progress: { received, expected in
            if received > 0 && !FileManager.default.fileExists(atPath: destination.path) {
                sawNetworkProgress.withLock { $0 = true }
                XCTAssertEqual(expected, Int64(bytes.count))
            }
        })
        XCTAssertTrue(sawNetworkProgress.withLock { $0 }, "progress must come from the transfer, not just its completion")
        XCTAssertEqual(try Data(contentsOf: destination), bytes)
    }

    private static func send(_ bytes: Data, offset: Int, on connection: NWConnection) {
        guard offset < bytes.count else { connection.cancel(); return }
        let end = min(offset + 16384, bytes.count)
        connection.send(content: bytes.subdata(in: offset..<end), completion: .contentProcessed { error in
            guard error == nil else { connection.cancel(); return }
            DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(10)) {
                send(bytes, offset: end, on: connection)
            }
        })
    }

    private func checkDownload(status: Int) async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: root); MockURLProtocol.handler = nil }
        let bytes = Data(repeating: 42, count: 128 * 1024)
        let hash = SHA256.hash(data: bytes).map { String(format: "%02x", $0) }.joined()
        MockURLProtocol.handler = { @Sendable request in
            if request.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"test"}"#.utf8)) }
            return (status, ["Content-Length": String(bytes.count)], bytes)
        }
        let index = try IndexStore(path: root.appendingPathComponent("index.sqlite").path)
        try index.apply([RemoteChange(seq: 1, op: "put", nodeID: "file", path: "/file.bin", isDir: false,
                                      version: 1, contentHash: hash, size: Int64(bytes.count), deleted: false)])
        let core = ProviderCore(index: index, client: APIClient(baseURL: URL(string: "https://test.invalid")!,
                                                              deviceToken: "test", session: MockURLProtocol.session()))
        let provider = FileProviderExtension(domain: NSFileProviderDomain(identifier: .init("DiscoDrive"), displayName: ""),
                                             makeCore: { core })
        let done = expectation(description: "download completed")
        let progress = provider.fetchContents(for: .init("file"), version: nil, request: NSFileProviderRequest()) { url, item, error in
            defer { done.fulfill(); if let url { try? FileManager.default.removeItem(at: url) } }
            if status == 200 {
                XCTAssertNil(error)
                XCTAssertEqual(item?.itemIdentifier.rawValue, "file")
                XCTAssertEqual(url.flatMap { try? Data(contentsOf: $0) }, bytes)
            } else {
                XCTAssertNotNil(error)
                XCTAssertNil(url)
            }
        }
        await fulfillment(of: [done], timeout: 10)
        XCTAssertEqual(progress.kind, .file)
        XCTAssertEqual(progress.fileOperationKind, .downloading)
        XCTAssertEqual(progress.totalUnitCount, Int64(bytes.count))
        if status == 200 { XCTAssertEqual(progress.fractionCompleted, 1) }
        else { XCTAssertLessThan(progress.fractionCompleted, 1) }
    }
}
