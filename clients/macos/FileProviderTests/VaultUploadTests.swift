import XCTest
import DiscoKit

final class VaultUploadTests: XCTestCase {
    func testRejectedEditKeepsLocalPlaintextAndRemovesOnlyConflictCiphertext() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let index = try IndexStore(path: root.appendingPathComponent("index.sqlite").path)
        let vault = try Vault(rawKeys: Data(repeating: 3, count: 64))
        let edited = Data(repeating: 7, count: 65539)
        let file = root.appendingPathComponent("edited")
        try edited.write(to: file)
        let deletion = expectation(description: "only conflict is removed")
        let upload = expectation(description: "encrypted upload guards old version")
        MockURLProtocol.handler = { request in
            switch (request.httpMethod, request.url!.path) {
            case ("POST", "/auth/device/token"):
                return (200, [:], Data(#"{"token":"jwt"}"#.utf8))
            case ("PUT", "/sync/file"):
                XCTAssertEqual(request.value(forHTTPHeaderField: "X-Base-Version"), "7")
                XCTAssertEqual(try? vault.decryptContent(MockURLProtocol.lastBody ?? Data()), edited)
                upload.fulfill()
                return (201, [:], Data(#"{"node":{"id":"conflict","version":1},"conflicted":true}"#.utf8))
            case ("DELETE", "/files/conflict"):
                deletion.fulfill()
                return (204, [:], Data())
            default:
                XCTFail("Unexpected request: \(request.httpMethod ?? "") \(request.url!.path)")
                return (500, [:], Data())
            }
        }
        defer { MockURLProtocol.handler = nil }
        let io = ServerVaultIO(vaultRoot: "/safe", index: index,
            client: APIClient(baseURL: URL(string: "https://test.invalid")!, deviceToken: "test", session: MockURLProtocol.session()))
        do {
            try await io.uploadFile(file, to: "entry.c9r", vault: vault, baseVersion: 7)
            XCTFail("A conflict must leave the edit pending in Files")
        } catch Vault.VaultError.nameTaken { }
        await fulfillment(of: [upload, deletion], timeout: 2)
        XCTAssertEqual(try Data(contentsOf: file), edited)
    }
}
