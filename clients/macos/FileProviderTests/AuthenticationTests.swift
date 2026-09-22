import XCTest
import FileProvider
import DiscoKit

final class AuthenticationTests: XCTestCase {
    private final class Factory: @unchecked Sendable {
        var attempts = 0 // Called under the extension's lock.
        let core: ProviderCore
        init(core: ProviderCore) { self.core = core }
        func make() throws -> ProviderCore {
            attempts += 1
            if attempts == 1 { throw NSFileProviderError(.cannotSynchronize) }
            return core
        }
    }

    private func core() throws -> ProviderCore {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: directory) }
        return try ProviderCore(index: IndexStore(path: directory.appendingPathComponent("index.sqlite").path),
                                client: APIClient(baseURL: URL(string: "https://example.invalid")!, deviceToken: "test"))
    }

    func testInitializationFailureIsRetriedWithoutRestartingExtension() throws {
        let factory = Factory(core: try core())
        let provider = FileProviderExtension(domain: NSFileProviderDomain(identifier: .init("DiscoDrive"), displayName: ""),
                                             makeCore: { try factory.make() })
        XCTAssertThrowsError(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest())) {
            XCTAssertEqual(($0 as NSError).code, NSFileProviderError.cannotSynchronize.rawValue)
        }
        XCTAssertNoThrow(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest()))
        XCTAssertNoThrow(try provider.enumerator(for: .rootContainer, request: NSFileProviderRequest()))
        XCTAssertEqual(factory.attempts, 2)
    }

    func testServerFailureDoesNotAskFinderToSignIn() async throws {
        let core = try core()
        for status in [429, 500, 503] {
            do {
                try await core.mapErrors { throw APIError.http(status) }
                XCTFail("Expected error")
            } catch let error as NSFileProviderError {
                XCTAssertEqual(error.code, .serverUnreachable)
            }
        }
    }
}
