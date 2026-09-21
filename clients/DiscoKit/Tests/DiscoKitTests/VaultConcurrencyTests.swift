import XCTest
@testable import DiscoKit

private actor DelayedVaultSource: VaultFileSource {
    let entries: [(name: String, isDir: Bool)]
    enum Failure: Error { case read }
    let fail: Bool
    var active = 0
    var peak = 0
    init(vault: Vault, fail: Bool = false) {
        self.fail = fail
        entries = (0..<17).map { (vault.encryptName("folder-\($0)", parentDirID: ""), true) }
    }
    func listDir(_ path: String) async throws -> [(name: String, isDir: Bool)] { entries }
    func read(_ path: String) async throws -> Data {
        active += 1
        peak = max(peak, active)
        defer { active -= 1 }
        try await Task.sleep(for: .milliseconds(20))
        if fail { throw Failure.read }
        return Data(path.utf8)
    }
}

final class VaultConcurrencyTests: XCTestCase {
    func testListingOverlapsMetadataReadsAndPreservesOrder() async throws {
        let vault = Vault(encKey: Array(repeating: 1, count: 32), macKey: Array(repeating: 2, count: 32))
        let source = DelayedVaultSource(vault: vault)
        let entries = try await vault.listEntries(dirID: "", source: source)
        let peak = await source.peak
        XCTAssertGreaterThan(peak, 1)
        XCTAssertLessThanOrEqual(peak, 6)
        XCTAssertEqual(entries.map(\.name), (0..<17).map { "folder-\($0)" })
    }

    func testFailedListingDrainsMetadataReads() async throws {
        let vault = Vault(encKey: Array(repeating: 1, count: 32), macKey: Array(repeating: 2, count: 32))
        let source = DelayedVaultSource(vault: vault, fail: true)
        do {
            _ = try await vault.listEntries(dirID: "", source: source)
            XCTFail("Expected read failure")
        } catch DelayedVaultSource.Failure.read {
            let active = await source.active
            XCTAssertEqual(active, 0)
        }
    }

}
