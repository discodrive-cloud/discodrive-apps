import XCTest
@testable import DiscoKit

final class VaultStreamingTests: XCTestCase {
    func testStreamingInteroperatesAtChunkBoundaries() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let vault = Vault(encKey: Array(repeating: 1, count: 32), macKey: Array(repeating: 2, count: 32))
        for size in [0, 1, 32767, 32768, 32769, 131089] {
            let data = Data((0..<size).map { UInt8($0 % 251) })
            let input = root.appendingPathComponent("input-\(size)")
            let encrypted = root.appendingPathComponent("encrypted-\(size)")
            let output = root.appendingPathComponent("output-\(size)")
            try data.write(to: input)
            try vault.encryptContent(from: input, to: encrypted)
            XCTAssertEqual(try vault.decryptContent(Data(contentsOf: encrypted)), data)
            try vault.encryptContent(data).write(to: encrypted)
            try vault.decryptContent(from: encrypted, to: output)
            XCTAssertEqual(try Data(contentsOf: output), data)
        }
    }

    func testCorruptionNeverPublishesPartialPlaintext() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let vault = Vault(encKey: Array(repeating: 1, count: 32), macKey: Array(repeating: 2, count: 32))
        var encrypted = try vault.encryptContent(Data(repeating: 7, count: 65537))
        encrypted[encrypted.count - 1] ^= 1
        let input = root.appendingPathComponent("input"), output = root.appendingPathComponent("output")
        try encrypted.write(to: input)
        XCTAssertThrowsError(try vault.decryptContent(from: input, to: output))
        XCTAssertFalse(FileManager.default.fileExists(atPath: output.path))
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: root.path), ["input"])
    }
}
