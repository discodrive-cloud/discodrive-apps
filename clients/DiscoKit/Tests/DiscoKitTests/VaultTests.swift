import XCTest
@testable import DiscoKit

final class VaultTests: XCTestCase {
    func cmvaultURL() -> URL {
        Bundle.module.resourceURL!.appendingPathComponent("Fixtures/cmvault")
    }

    // End-to-end interop: open a real vault created by Cryptomator (password "password123").
    // Exercises scrypt + AES-KW + masterkey + JWT together.
    func testOpenRealCryptomatorVault() throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        XCTAssertEqual(v.encKey.count, 32)
        XCTAssertEqual(v.macKey.count, 32)
    }

    func testWrongPassword() {
        XCTAssertThrowsError(try Vault.open(directory: cmvaultURL(), password: "wrong")) { err in
            XCTAssertEqual(err as? Vault.VaultError, .wrongPassword)
        }
    }

    // DirIdHash("") must point to the vault's actual root directory on disk.
    /// The scrypt parameters come from masterkey.cryptomator, which anyone who can write the
    /// vault folder controls: N = 0 divided by zero inside ROMix, a huge N·r asked for
    /// gigabytes. Out-of-range values are refused before any derivation.
    func testHostileScryptParametersAreRefused() throws {
        let mkURL = cmvaultURL().appendingPathComponent("masterkey.cryptomator")
        let jwt = try String(contentsOf: cmvaultURL().appendingPathComponent("vault.cryptomator"), encoding: .utf8)
        let original = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: mkURL)) as? [String: Any])
        for (n, r) in [(0, 8), (-1, 8), (3, 8), (1 << 21, 8), (32768, 0), (32768, 33), (32768, -8)] {
            var mk = original
            mk["scryptCostParam"] = n
            mk["scryptBlockSize"] = r
            let data = try JSONSerialization.data(withJSONObject: mk)
            XCTAssertThrowsError(try Vault.open(masterkeyData: data, vaultJWT: jwt, password: "password123"),
                                 "n=\(n) r=\(r)") { err in
                XCTAssertEqual(err as? Vault.VaultError, .invalidScryptParameters(n: n, r: r, p: 1))
            }
        }
    }

    func testScryptParameterBounds() throws {
        XCTAssertNoThrow(try Vault.validateScryptParameters(n: 1, r: 1, p: 1))
        XCTAssertNoThrow(try Vault.validateScryptParameters(n: 32768, r: 8, p: 1))
        XCTAssertNoThrow(try Vault.validateScryptParameters(n: 1 << 20, r: 32, p: 4))
        XCTAssertThrowsError(try Vault.validateScryptParameters(n: 1 << 21, r: 8, p: 1))
        XCTAssertThrowsError(try Vault.validateScryptParameters(n: 6, r: 8, p: 1))
        XCTAssertThrowsError(try Vault.validateScryptParameters(n: 1024, r: 8, p: 0))
    }

    func testDirIdHashRootExists() throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        let hash = v.dirIdHash("")
        let path = cmvaultURL().appendingPathComponent(hash)
        XCTAssertTrue(FileManager.default.fileExists(atPath: path.path), "DirIdHash(\"\") = \(hash) does not exist")
    }

    func testNameRoundTrip() throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        for (name, parent) in [("файл.txt", ""), ("Документ.pdf", "some-dir-id"), ("a", "")] {
            let enc = v.encryptName(name, parentDirID: parent)
            XCTAssertTrue(enc.hasSuffix(".c9r"))
            XCTAssertEqual(try v.decryptName(enc, parentDirID: parent), name)
        }
    }

    func testContentRoundTripMultiChunk() throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        for size in [0, 10, Vault.chunkPlainSize, Vault.chunkPlainSize + 5, 3 * Vault.chunkPlainSize + 123] {
            let data = Data((0..<size).map { UInt8($0 & 0xff) })
            let enc = try v.encryptContent(data)
            XCTAssertEqual(try v.decryptContent(enc), data, "size=\(size)")
        }
    }

    // Collect the entire vault tree into a [path → content] map (recursive walk).
    func collectTree(_ v: Vault, _ io: VaultFileSource) async throws -> [String: Data] {
        var files = [String: Data]()
        func walk(_ dirID: String, _ prefix: String) async throws {
            for e in try await v.listEntries(dirID: dirID, source: io) {
                if e.isDir { try await walk(e.dirID!, prefix + e.name + "/") }
                else if let cp = e.contentPath { files[prefix + e.name] = try await v.decryptFile(at: cp, source: io) }
            }
        }
        try await walk("", "")
        return files
    }

    // MILESTONE: open a real Cryptomator vault and find the expected plaintext content.
    func testInteropDecryptRealVault() async throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        let files = try await collectTree(v, LocalVaultIO(root: cmvaultURL()))
        XCTAssertFalse(files.isEmpty)
        let found = files.values.contains { String(data: $0, encoding: .utf8)?.contains("Привет, мой верный друг!") ?? false }
        XCTAssertTrue(found, "expected content not found. Files: \(files.keys)")
    }

    // Recovery key: 44 words, round-trips back to keys, CRC catches corruption.
    func testRecoveryKeyRoundTrip() throws {
        let v = try Vault.open(directory: cmvaultURL(), password: "password123")
        let phrase = v.recoveryKey()
        XCTAssertEqual(phrase.split(separator: " ").count, 44)
        let (e, m) = try Vault.keysFromRecovery(phrase)
        XCTAssertEqual(e, v.encKey)
        XCTAssertEqual(m, v.macKey)
        var words = phrase.split(separator: " ").map(String.init)
        words[0] = (words[0] == "ad") ? "ah" : "ad"   // different valid word → CRC mismatch
        XCTAssertThrowsError(try Vault.keysFromRecovery(words.joined(separator: " ")))
    }

    // Rename and delete operations inside a vault.
    func testVaultRenameAndDelete() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let io = LocalVaultIO(root: dir)
        let v = try await Vault.create(sink: io, password: "p")
        try await v.addFile(name: "old.txt", data: Data("hi".utf8), parentDirID: "", sink: io)
        let subID = try await v.createFolder(name: "Папка", parentDirID: "", sink: io)
        try await v.addFile(name: "inner.txt", data: Data("вглубине".utf8), parentDirID: subID, sink: io)

        // Rename a file.
        let fileEntry = try await v.listEntries(dirID: "", source: io).first { $0.name == "old.txt" }!
        try await v.renameEntry(fileEntry, to: "new.txt", parentDirID: "", source: io, sink: io)
        var files = try await collectTree(v, io)
        XCTAssertNil(files["old.txt"]); XCTAssertEqual(files["new.txt"], Data("hi".utf8))

        // Rename a folder (contents inside must be preserved).
        let folderEntry = try await v.listEntries(dirID: "", source: io).first { $0.name == "Папка" }!
        try await v.renameEntry(folderEntry, to: "Архив", parentDirID: "", source: io, sink: io)
        files = try await collectTree(v, io)
        XCTAssertEqual(files["Архив/inner.txt"], Data("вглубине".utf8))
        XCTAssertNil(files["Папка/inner.txt"])

        // Delete a folder recursively.
        let arch = try await v.listEntries(dirID: "", source: io).first { $0.name == "Архив" }!
        try await v.deleteEntry(arch, source: io, sink: io)
        files = try await collectTree(v, io)
        XCTAssertNil(files["Архив/inner.txt"])
        XCTAssertEqual(files["new.txt"], Data("hi".utf8))   // file survived
    }

    // Write path: create a vault, add files/folders, reopen it, and read back.
    func testCreateVaultWriteReadRoundTrip() async throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let io = LocalVaultIO(root: dir)

        let v = try await Vault.create(sink: io, password: "secret-pass")
        try await v.addFile(name: "привет.txt", data: Data("Привет, мир!".utf8), parentDirID: "", sink: io)
        let subID = try await v.createFolder(name: "Папка с документами", parentDirID: "", sink: io)
        let big = Data((0..<(Vault.chunkPlainSize * 2 + 7)).map { UInt8($0 & 0xff) })
        try await v.addFile(name: "большой.bin", data: big, parentDirID: subID, sink: io)

        // Reopen with the same password (validates Create: masterkey/scrypt/KW/JWT).
        let v2 = try Vault.open(directory: dir, password: "secret-pass")
        XCTAssertThrowsError(try Vault.open(directory: dir, password: "wrong"))

        let files = try await collectTree(v2, io)
        XCTAssertEqual(files["привет.txt"], Data("Привет, мир!".utf8))
        XCTAssertEqual(files["Папка с документами/большой.bin"], big)
    }
}
