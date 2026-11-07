import XCTest
@testable import DiscoKit

final class LinkShareTests: XCTestCase {
    func testFirstWebURLInText() {
        XCTAssertEqual(LinkShare.firstWebURL(in: "Посмотрите https://www.threads.com/@a/post/1 !")?.absoluteString,
                       "https://www.threads.com/@a/post/1")
        XCTAssertEqual(LinkShare.firstWebURL(in: "рецепт: https://www.instagram.com/reel/ABC/?igsh=x")?.absoluteString,
                       "https://www.instagram.com/reel/ABC/?igsh=x")
        XCTAssertEqual(LinkShare.firstWebURL(in: "Пост на threads.net https://www.threads.net/@a/post/1")?.absoluteString,
                       "https://www.threads.net/@a/post/1", "a bare domain is not the shared link")
        XCTAssertNil(LinkShare.firstWebURL(in: "заходите на example.com"))
        XCTAssertNil(LinkShare.firstWebURL(in: "ftp://x.org/a"))
        XCTAssertNil(LinkShare.firstWebURL(in: "javascript:alert(1)"))
        XCTAssertNil(LinkShare.firstWebURL(in: "просто текст"))
    }

    func testOutcomeByStatus() {
        XCTAssertEqual(LinkShare.outcome(status: 200), .saved)
        XCTAssertEqual(LinkShare.outcome(status: nil), .queued)
        XCTAssertEqual(LinkShare.outcome(status: 503), .queued)
        XCTAssertEqual(LinkShare.outcome(status: 429), .queued)
        XCTAssertEqual(LinkShare.outcome(status: 401), .signIn)
        XCTAssertEqual(LinkShare.outcome(status: 400), .rejected)
    }

    func testMessagesAreFormal() {
        XCTAssertEqual(LinkShare.message(for: .saved, language: "ru"), "✓ Сохранено в DiscoDrive")
        XCTAssertEqual(LinkShare.message(for: .queued, language: "ru"), "✓ Сохранено в DiscoDrive")
        XCTAssertEqual(LinkShare.message(for: .signIn, language: "ru"), "Сначала войдите в DiscoDrive")
        XCTAssertEqual(LinkShare.message(for: .rejected, language: "ru"), "Эту ссылку нельзя сохранить")
        XCTAssertEqual(LinkShare.message(for: .signIn, language: "en"), "Sign in to DiscoDrive first")
    }

    private func tempDir() throws -> URL {
        let d = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: d) }
        return d
    }

    func testOutboxPersistsAndRemoves() throws {
        let dir = try tempDir()
        let box = LinkOutbox(directory: dir)
        box.enqueue(URL(string: "https://a.test/1")!, title: "one")
        box.enqueue(URL(string: "https://a.test/2")!, title: nil)
        XCTAssertEqual(box.pending().map(\.url.absoluteString), ["https://a.test/1", "https://a.test/2"])
        box.remove([box.pending()[0].id])
        XCTAssertEqual(LinkOutbox(directory: dir).pending().map(\.url.absoluteString), ["https://a.test/2"])
    }

    func testOutboxIsBounded() throws {
        let box = LinkOutbox(directory: try tempDir())
        for i in 0...LinkOutbox.capacity { box.enqueue(URL(string: "https://a.test/\(i)")!, title: nil) }
        let urls = box.pending().map(\.url.absoluteString)
        XCTAssertEqual(urls.count, LinkOutbox.capacity)
        XCTAssertEqual(urls.first, "https://a.test/1", "the oldest entry gives way")
    }

    func testSaveLinkAndFlush() async throws {
        var posted: [String] = []
        let lock = NSLock()
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            XCTAssertEqual(req.httpMethod, "POST")
            XCTAssertEqual(req.url!.path, "/me/links")
            XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer J")
            let body = MockURLProtocol.lastBody ?? Data()
            let json = try! JSONSerialization.jsonObject(with: body) as! [String: Any]
            let url = json["url"] as! String
            if url.hasPrefix("https://a.test/") && url != "https://a.test/ok" {
                XCTAssertNotNil(json["shared_at"] as? String, "queued links carry the time they were shared")
            }
            lock.lock(); posted.append(url); lock.unlock()
            switch url {
            case "https://a.test/bad": return (400, [:], Data(#"{"error":"url is not allowed"}"#.utf8))
            case "https://a.test/down": return (503, [:], Data())
            default: return (200, [:], Data(#"{"id":"x"}"#.utf8))
            }
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        let status = try await client.saveLink(url: URL(string: "https://a.test/ok")!, title: "t")
        XCTAssertEqual(status, 200)

        let box = LinkOutbox(directory: try tempDir())
        for u in ["https://a.test/1", "https://a.test/bad", "https://a.test/down", "https://a.test/2"] {
            box.enqueue(URL(string: u)!, title: nil)
        }
        await LinkShare.flush(outbox: box, client: client)
        // Sent in order; 2xx and 400 leave the queue, a 5xx stops the flush and stays.
        XCTAssertEqual(box.pending().map(\.url.absoluteString), ["https://a.test/down", "https://a.test/2"])
        XCTAssertEqual(posted, ["https://a.test/ok", "https://a.test/1", "https://a.test/bad", "https://a.test/down"])
    }

    func testFlushStopsWhenCancelled() async throws {
        MockURLProtocol.handler = { req in
            if req.url!.path == "/auth/device/token" { return (200, [:], Data(#"{"token":"J"}"#.utf8)) }
            return (200, [:], Data("{}".utf8))
        }
        let client = APIClient(baseURL: URL(string: "https://server.test")!, deviceToken: "device", session: MockURLProtocol.session())
        let box = LinkOutbox(directory: try tempDir())
        for i in 0..<5 { box.enqueue(URL(string: "https://c.test/\(i)")!, title: nil) }
        let task = Task { await LinkShare.flush(outbox: box, client: client) }
        task.cancel()
        await task.value
        XCTAssertEqual(box.pending().count, 5, "a cancelled flush sends nothing more")
    }
}
