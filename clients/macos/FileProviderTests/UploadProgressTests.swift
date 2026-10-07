import XCTest
import Network
import os
import DiscoKit

final class UploadProgressTests: XCTestCase {
    func testSingleRequestReportsBytesBeforeServerAcknowledgement() async throws {
        try await checkUpload(chunked: false)
    }

    func testChunkedRequestReportsBytesBeforeFirstChunkAcknowledgement() async throws {
        try await checkUpload(chunked: true)
    }

    private func checkUpload(chunked: Bool) async throws {
        let file = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let size = 256 * 1024
        try Data(repeating: 42, count: size).write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        let replies = OSAllocatedUnfairLock(initialState: 0)
        let sawProgress = OSAllocatedUnfairLock(initialState: false)
        let listener = try NWListener(using: .tcp, on: .any)
        let ready = expectation(description: "HTTP server ready")
        listener.stateUpdateHandler = { state in if case .ready = state { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            connection.start(queue: .global())
            Self.receive(on: connection, buffered: Data(), replies: replies)
        }
        listener.start(queue: .global())
        defer { listener.cancel() }
        await fulfillment(of: [ready], timeout: 5)
        let port = try XCTUnwrap(listener.port).rawValue
        let client = APIClient(baseURL: URL(string: "http://127.0.0.1:\(port)")!, deviceToken: "test",
                               session: URLSession(configuration: .ephemeral))
        let result = try await client.upload(fileURL: file, relPath: "/file", modifiedAt: nil,
                                             chunkSize: chunked ? 65536 : size * 2, progress: { sent, total in
            XCTAssertEqual(total, Int64(size))
            XCTAssertGreaterThanOrEqual(sent, 0)
            XCTAssertLessThanOrEqual(sent, Int64(size))
            if sent > 0 && replies.withLock({ $0 == 0 }) { sawProgress.withLock { $0 = true } }
        })
        XCTAssertEqual(result.nodeID, "file")
        XCTAssertTrue(sawProgress.withLock { $0 }, "must report bytes before the first PUT response, not just after it")
        XCTAssertEqual(replies.withLock { $0 }, chunked ? 4 : 1)
    }

    private static func receive(on connection: NWConnection, buffered: Data,
                                replies: OSAllocatedUnfairLock<Int>) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { data, _, complete, error in
            guard error == nil, let data, !data.isEmpty else { connection.cancel(); return }
            var buffer = buffered
            buffer.append(data)
            if let separator = buffer.range(of: Data("\r\n\r\n".utf8)) {
                let header = String(decoding: buffer[..<separator.lowerBound], as: UTF8.self)
                let lines = header.components(separatedBy: "\r\n")
                let length = lines.first { $0.lowercased().hasPrefix("content-length:") }
                    .flatMap { Int($0.split(separator: ":", maxSplits: 1)[1].trimmingCharacters(in: .whitespaces)) } ?? 0
                if buffer.count - separator.upperBound >= length {
                    let line = lines[0]
                    let put = line.hasPrefix("PUT ")
                    let body: String
                    if line.contains("/auth/device/token") { body = #"{"token":"test"}"# }
                    else if line.contains("/upload/init") { body = #"{"upload_id":"u","next_chunk":0}"# }
                    else if line.contains("/chunk/") {
                        let path = line.split(separator: " ")[1]
                        let index = Int(path.split(separator: "/").last!)!
                        body = "{\"next_chunk\":\(index + 1)}"
                    } else { body = #"{"node":{"id":"file","version":1}}"# }
                    DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(100)) {
                        if put { replies.withLock { $0 += 1 } }
                        let code = put || line.contains("/complete") ? 201 : 200
                        let response = Data("HTTP/1.1 \(code) OK\r\nContent-Length: \(body.utf8.count)\r\nConnection: close\r\n\r\n\(body)".utf8)
                        connection.send(content: response, completion: .contentProcessed { _ in connection.cancel() })
                    }
                    return
                }
            }
            if complete { connection.cancel() }
            else { receive(on: connection, buffered: buffer, replies: replies) }
        }
    }
}
