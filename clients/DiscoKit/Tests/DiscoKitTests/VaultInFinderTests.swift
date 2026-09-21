import XCTest
import GRDB
@testable import DiscoKit

// What the File Provider extension needs to present an unlocked vault as its own Finder
// location: the vault's keys as bytes it can be rebuilt from, item identifiers that
// survive a restart, and the directory map Cryptomator itself does not keep.
final class VaultInFinderTests: XCTestCase {
    func testVaultRoundTripsThroughItsRawKeys() throws {
        let enc = [UInt8](repeating: 0x11, count: 32), mac = [UInt8](repeating: 0x22, count: 32)
        let v = Vault(encKey: enc, macKey: mac)
        let raw = v.rawKeys
        XCTAssertEqual(raw.count, 64)
        let back = try Vault(rawKeys: raw)
        // Same keys encrypt a name to the same ciphertext.
        XCTAssertEqual(back.encryptName("a.txt", parentDirID: ""), v.encryptName("a.txt", parentDirID: ""))
        XCTAssertThrowsError(try Vault(rawKeys: Data(count: 10)))
    }

    func testItemIdentifiersEncodeWhatARestartNeeds() {
        XCTAssertEqual(VaultItemID.root, VaultItemID.decode(VaultItemID.encode(.root)))
        let dir = VaultItemID.dir(dirID: "5f3a")
        XCTAssertEqual(VaultItemID.decode(VaultItemID.encode(dir)), dir)
        let file = VaultItemID.file(parentDirID: "", nodeID: "n-1")
        XCTAssertEqual(VaultItemID.decode(VaultItemID.encode(file)), file)
        let deep = VaultItemID.file(parentDirID: "5f3a", nodeID: "n-2")
        XCTAssertEqual(VaultItemID.decode(VaultItemID.encode(deep)), deep)
        XCTAssertNil(VaultItemID.decode("garbage"))
        // The root directory's id is the root container's own, so Finder treats it as such.
        XCTAssertEqual(VaultItemID.encode(.root), ProviderItemInfo.rootIdentifier)
    }

    func testDirectoryMapRemembersParentsAndStorage() throws {
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.rememberVaultDir(vault: "v1", dirID: "d1", parentDirID: "", name: "docs", entryNodeID: "n-docs")
        try store.rememberVaultDir(vault: "v1", dirID: "d2", parentDirID: "d1", name: "inner", entryNodeID: "n-inner")
        let d2 = try XCTUnwrap(try store.vaultDir(vault: "v1", dirID: "d2"))
        XCTAssertEqual(d2.parentDirID, "d1")
        XCTAssertEqual(d2.name, "inner")
        XCTAssertEqual(d2.entryNodeID, "n-inner")
        XCTAssertNil(try store.vaultDir(vault: "v2", dirID: "d2"), "maps are per vault")
        // A rename records the new name in place.
        try store.rememberVaultDir(vault: "v1", dirID: "d2", parentDirID: "d1", name: "renamed", entryNodeID: "n-inner2")
        XCTAssertEqual(try store.vaultDir(vault: "v1", dirID: "d2")?.name, "renamed")
        XCTAssertEqual(try store.vaultDirs(vault: "v1").count, 2)
        try store.forgetVaultDirs(vault: "v1")
        XCTAssertEqual(try store.vaultDirs(vault: "v1").count, 0)
    }

    // A sink that only remembers what was written where.
    final class RecordingSink: VaultFileSink, @unchecked Sendable {
        var files: [String] = [], dirs: [String] = []
        func makeDir(_ relPath: String) async throws { dirs.append(relPath) }
        func writeFile(_ relPath: String, _ data: Data) async throws { files.append(relPath) }
        func remove(_ relPath: String) async throws {}
    }

    func testEntryPathIsWhereTheWritersPutTheEntry() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let sink = RecordingSink()
        try await v.addFile(name: "a.txt", data: Data("x".utf8), parentDirID: "", sink: sink)
        XCTAssertEqual(sink.files, [v.entryPath(name: "a.txt", parentDirID: "")])
        try await v.createFolder(name: "docs", parentDirID: "p1", sink: sink)
        XCTAssertEqual(sink.dirs.first, v.entryPath(name: "docs", parentDirID: "p1"))
        // A name past Cryptomator's shortening threshold lands in a .c9s wrapper folder.
        let long = String(repeating: "n", count: 200) + ".txt"
        try await v.addFile(name: long, data: Data(), parentDirID: "", sink: sink)
        XCTAssertTrue(v.entryPath(name: long, parentDirID: "").hasSuffix(".c9s"))
        XCTAssertEqual(sink.dirs.last, v.entryPath(name: long, parentDirID: ""))
    }

    // A vault held in memory: enough of a file system for VaultWrite to work against.
    final class MemoryVaultIO: VaultFileSource, VaultFileSink, @unchecked Sendable {
        var files: [String: Data] = [:]
        var dirs: Set<String> = [""]
        func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
            let prefix = relPath.isEmpty ? "" : relPath + "/"
            var out: [String: Bool] = [:]
            for f in files.keys where f.hasPrefix(prefix) {
                let rest = f.dropFirst(prefix.count)
                if let slash = rest.firstIndex(of: "/") { out[String(rest[..<slash])] = true } else { out[String(rest)] = false }
            }
            for d in dirs where d.hasPrefix(prefix) && d != relPath {
                let rest = d.dropFirst(prefix.count)
                out[String(rest.split(separator: "/").first ?? "")] = true
            }
            return out.map { ($0.key, $0.value) }
        }
        func read(_ relPath: String) async throws -> Data {
            guard let d = files[relPath] else { throw NSError(domain: "mem", code: 404) }
            return d
        }
        func makeDir(_ relPath: String) async throws {
            var acc = ""
            for p in relPath.split(separator: "/") { acc = acc.isEmpty ? String(p) : acc + "/" + p; dirs.insert(acc) }
        }
        func writeFile(_ relPath: String, _ data: Data) async throws { files[relPath] = data }
        func remove(_ relPath: String) async throws {
            files = files.filter { !($0.key == relPath || $0.key.hasPrefix(relPath + "/")) }
            dirs = dirs.filter { !($0 == relPath || $0.hasPrefix(relPath + "/")) }
        }
    }

    func testMovingAFileReEncryptsItUnderTheNewParent() async throws {
        let io = MemoryVaultIO()
        let v = try await Vault.create(sink: io, password: "pw")
        let docs = try await v.createFolder(name: "docs", parentDirID: "", sink: io)
        try await v.addFile(name: "a.txt", data: Data("hello".utf8), parentDirID: "", sink: io)
        let entry = try await v.listEntries(dirID: "", source: io).first { $0.name == "a.txt" }!
        try await v.moveEntry(entry, from: "", to: docs, source: io, sink: io)
        let root = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(root.map(\.name), ["docs"])
        let moved = try await v.listEntries(dirID: docs, source: io)
        XCTAssertEqual(moved.map(\.name), ["a.txt"])
        let data = try await v.decryptFile(at: moved[0].contentPath!, source: io)
        XCTAssertEqual(data, Data("hello".utf8))
    }

    func testMovingADirectoryKeepsItsSubtree() async throws {
        let io = MemoryVaultIO()
        let v = try await Vault.create(sink: io, password: "pw")
        let a = try await v.createFolder(name: "a", parentDirID: "", sink: io)
        let b = try await v.createFolder(name: "b", parentDirID: "", sink: io)
        try await v.addFile(name: "deep.txt", data: Data("x".utf8), parentDirID: a, sink: io)
        let entry = try await v.listEntries(dirID: "", source: io).first { $0.name == "a" }!
        try await v.moveEntry(entry, from: "", to: b, as: "renamed-a", source: io, sink: io)
        let root = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(root.map(\.name), ["b"])
        let inB = try await v.listEntries(dirID: b, source: io)
        XCTAssertEqual(inB.map(\.name), ["renamed-a"])
        XCTAssertEqual(inB[0].dirID, a, "the directory keeps its id, so its storage and children stay put")
        let inA = try await v.listEntries(dirID: a, source: io)
        XCTAssertEqual(inA.map(\.name), ["deep.txt"])
    }

    func testVaultKeyStoreQueriesUseTheSharedGroup() {
        let saved = KeychainConfig.accessGroup
        defer { KeychainConfig.accessGroup = saved }
        KeychainConfig.accessGroup = "TEAM.grp"
        let q = VaultKeyStore.query(vaultID: "node-1")
        XCTAssertEqual(q[kSecAttrService as String] as? String, VaultKeyStore.service)
        XCTAssertEqual(q[kSecAttrAccount as String] as? String, "node-1")
        XCTAssertEqual(q[kSecAttrAccessGroup as String] as? String, "TEAM.grp")
    }
    // MARK: - Review fixes

    func testChangedCiphertextPathsNameTheDirectoriesTheyAreIn() {
        // dirIdHash already starts with "d/": the lookup used to prepend a second one and
        // matched nothing, so Finder never heard about changes inside an open vault.
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        XCTAssertTrue(v.dirIdHash("").hasPrefix("d/"))
        let changed = [v.dirIdHash("") + "/abc.c9r", v.dirIdHash("sub-1") + "/long.c9s/contents.c9r", "masterkey.cryptomator"]
        XCTAssertEqual(v.dirIDs(touchedBy: changed, knownDirIDs: ["sub-1", "sub-2"]), ["", "sub-1"])
        XCTAssertEqual(v.dirIDs(touchedBy: [v.dirIdHash("unknown") + "/x.c9r"], knownDirIDs: ["sub-1"]), [])
    }

    func testALongNamedFileTakesSizeAndVersionFromItsContents() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let io = MemoryVaultIO()
        let long = String(repeating: "n", count: 200) + ".txt"
        try await v.addFile(name: long, data: Data("x".utf8), parentDirID: "", sink: io)
        try await v.addFile(name: "short.txt", data: Data("y".utf8), parentDirID: "", sink: io)
        try await v.createFolder(name: "folder", parentDirID: "", sink: io)
        let entries = try await v.listEntries(dirID: "", source: io)
        let longEntry = try XCTUnwrap(entries.first { $0.name == long })
        XCTAssertTrue(longEntry.encPath.hasSuffix(".c9s"))
        XCTAssertEqual(longEntry.nodePath, longEntry.encPath + "/contents.c9r", "not the wrapper folder: it has no size and never changes")
        let short = try XCTUnwrap(entries.first { $0.name == "short.txt" })
        XCTAssertEqual(short.nodePath, short.encPath)
        let folder = try XCTUnwrap(entries.first { $0.name == "folder" })
        XCTAssertEqual(folder.nodePath, folder.encPath)
    }
}
