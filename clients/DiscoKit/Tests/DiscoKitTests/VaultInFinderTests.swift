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

    /// A directory id is whatever dir.c9r holds, and any writer of the vault chooses it; one
    /// with a ':' cut the identifier at the wrong place and every file under it vanished.
    func testFileIdentifierSurvivesAColonInTheDirectoryID() {
        for dirID in ["a:b", ":", "x:y:z", "trailing:", ":leading"] {
            let id = VaultItemID.file(parentDirID: dirID, nodeID: "3f2c9a1e-0b7d-4c55-9a51-2f7e1d0c8b44")
            XCTAssertEqual(VaultItemID.decode(VaultItemID.encode(id)), id, dirID)
        }
        // Identifiers Finder already holds keep decoding as before.
        XCTAssertEqual(VaultItemID.decode("file:5f3a:n-2"), .file(parentDirID: "5f3a", nodeID: "n-2"))
        XCTAssertEqual(VaultItemID.decode("file::n-1"), .file(parentDirID: "", nodeID: "n-1"))
        XCTAssertNil(VaultItemID.decode("file:no-colon"))
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
        func createFile(_ relPath: String, _ data: Data) async throws {
            guard !files.contains(relPath) else { throw Vault.VaultError.nameTaken(relPath) }
            files.append(relPath)
        }
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
        // What another client wrote and this one's index has not heard of yet: there for a
        // write to run into, absent from every listing.
        var unseen: Set<String> = []
        private func seen(_ path: String) -> Bool { !unseen.contains { path == $0 || path.hasPrefix($0 + "/") } }
        func createFile(_ relPath: String, _ data: Data) async throws {
            if files[relPath] != nil { throw Vault.VaultError.nameTaken(relPath) }
            files[relPath] = data
        }
        func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
            let prefix = relPath.isEmpty ? "" : relPath + "/"
            var out: [String: Bool] = [:]
            let files = self.files.filter { seen($0.key) }, dirs = self.dirs.filter(seen)
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
    // MARK: - A rename or move onto a taken name

    func testRenamingAFolderOntoAnotherIsRefusedAndBothKeepTheirContents() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let io = MemoryVaultIO()
        let a = try await v.createFolder(name: "A", parentDirID: "", sink: io)
        let b = try await v.createFolder(name: "B", parentDirID: "", sink: io)
        try await v.addFile(name: "in-b.txt", data: Data("b".utf8), parentDirID: b, sink: io)
        let before = try await v.listEntries(dirID: "", source: io)
        let entryA = try XCTUnwrap(before.first { $0.name == "A" })
        do {
            try await v.renameEntry(entryA, to: "B", parentDirID: "", source: io, sink: io)
            XCTFail("a taken name must be refused")
        } catch let e as Vault.VaultError { XCTAssertEqual(e, .nameTaken("B")) }
        let root = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(root.first { $0.name == "B" }?.dirID, b, "B still points at its own directory")
        XCTAssertEqual(root.first { $0.name == "A" }?.dirID, a)
        let inB = try await v.listEntries(dirID: b, source: io).map(\.name)
        XCTAssertEqual(inB, ["in-b.txt"])
    }

    func testMovingOntoATakenNameIsRefusedForFilesAndFolders() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let io = MemoryVaultIO()
        let dst = try await v.createFolder(name: "dst", parentDirID: "", sink: io)
        try await v.addFile(name: "x.txt", data: Data("theirs".utf8), parentDirID: dst, sink: io)
        try await v.createFolder(name: "sub", parentDirID: dst, sink: io)
        try await v.addFile(name: "x.txt", data: Data("ours".utf8), parentDirID: "", sink: io)
        try await v.createFolder(name: "sub", parentDirID: "", sink: io)
        for name in ["x.txt", "sub"] {
            let listed = try await v.listEntries(dirID: "", source: io)
            let e = try XCTUnwrap(listed.first { $0.name == name })
            do {
                try await v.moveEntry(e, from: "", to: dst, source: io, sink: io)
                XCTFail("\(name): a taken name must be refused")
            } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken(name)) }
        }
        let inDst = try await v.listEntries(dirID: dst, source: io)
        let theirs = try XCTUnwrap(inDst.first { $0.name == "x.txt" })
        let kept = try await v.decryptFile(at: try XCTUnwrap(theirs.contentPath), source: io)
        XCTAssertEqual(kept, Data("theirs".utf8))
        let rootNames = try await v.listEntries(dirID: "", source: io).map(\.name).sorted()
        XCTAssertEqual(rootNames, ["dst", "sub", "x.txt"], "nothing left its place")
    }
    // MARK: - A name taken between the look and the write

    func testADestinationTakenAfterTheCheckIsNotWrittenOverAndTheSourceStays() async throws {
        let v = Vault(encKey: [UInt8](repeating: 0x11, count: 32), macKey: [UInt8](repeating: 0x22, count: 32))
        let io = MemoryVaultIO()
        let a = try await v.createFolder(name: "A", parentDirID: "", sink: io)
        try await v.addFile(name: "f.txt", data: Data("ours".utf8), parentDirID: "", sink: io)
        // Another client creates folder B and file g.txt; this one's index has not heard.
        let b = try await v.createFolder(name: "B", parentDirID: "", sink: io)
        try await v.addFile(name: "in-b.txt", data: Data("b".utf8), parentDirID: b, sink: io)
        try await v.addFile(name: "g.txt", data: Data("theirs".utf8), parentDirID: "", sink: io)
        io.unseen = [v.entryPath(name: "B", parentDirID: ""), v.entryPath(name: "g.txt", parentDirID: "")]

        let listed = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(listed.map(\.name).sorted(), ["A", "f.txt"], "the look before the write sees nothing in the way")
        for (from, to) in [("A", "B"), ("f.txt", "g.txt")] {
            let e = try XCTUnwrap(listed.first { $0.name == from })
            do {
                try await v.renameEntry(e, to: to, parentDirID: "", source: io, sink: io)
                XCTFail("\(to): the write itself must refuse a taken name")
            } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken(to)) }
        }
        do {
            try await v.createFolder(name: "B", parentDirID: "", sink: io)
            XCTFail("a new folder must not take over an existing one's name")
        } catch let err as Vault.VaultError { XCTAssertEqual(err, .nameTaken("B")) }

        io.unseen = []
        let root = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(root.map(\.name).sorted(), ["A", "B", "f.txt", "g.txt"], "the sources were not removed")
        XCTAssertEqual(root.first { $0.name == "B" }?.dirID, b, "B still points at its own directory")
        XCTAssertEqual(root.first { $0.name == "A" }?.dirID, a)
        let g = try XCTUnwrap(root.first { $0.name == "g.txt" })
        let kept = try await v.decryptFile(at: try XCTUnwrap(g.contentPath), source: io)
        XCTAssertEqual(kept, Data("theirs".utf8))
    }

    func testADirectoryFoundGoneIsReportedAgainForTheSameDelta() throws {
        // Finder knows a vault folder by its directory id; once the map row is dropped
        // nothing leads from the deleted node back to it. The system may ask for the same
        // delta twice, or a second enumerator may ask from an older anchor later.
        let store = try IndexStore(dbQueue: DatabaseQueue())
        try store.rememberVaultDir(vault: "v", dirID: "d1", parentDirID: "", name: "docs", entryNodeID: "n1")
        try store.markVaultDirGone(vault: "v", dirID: "d1", at: 120)
        XCTAssertEqual(try store.vaultDirs(vault: "v").count, 0, "gone for listings and lookups")
        XCTAssertNil(try store.vaultDir(vault: "v", dirID: "d1"))
        XCTAssertEqual(try store.vaultDirsGone(vault: "v"), ["d1"])
        XCTAssertEqual(try store.vaultDirsGone(vault: "v", since: 100), ["d1"])
        XCTAssertEqual(try store.vaultDirsGone(vault: "v", since: 120), ["d1"])
        XCTAssertEqual(try store.vaultDirsGone(vault: "v", since: 121), [])
        // A newer enumerator cannot consume the deletion for an older one.
        XCTAssertEqual(try store.vaultDirsGone(vault: "v", since: 100), ["d1"])
        // A directory that comes back (moved back in, restored) is a directory again.
        try store.rememberVaultDir(vault: "v", dirID: "d2", parentDirID: "", name: "x", entryNodeID: "n2")
        try store.markVaultDirGone(vault: "v", dirID: "d2", at: 130)
        try store.rememberVaultDir(vault: "v", dirID: "d2", parentDirID: "", name: "x", entryNodeID: "n3")
        XCTAssertEqual(try store.vaultDirsGone(vault: "v"), ["d1"])
        XCTAssertEqual(try store.vaultDir(vault: "v", dirID: "d2")?.entryNodeID, "n3")
    }
}

/// Renaming or moving a vault file re-encrypts it (Cryptomator names are bound to the
/// parent directory). Done in memory, a 2 GB video took the File Provider extension down;
/// against a source and sink that can stream, the contents go file to file instead.
final class VaultStreamingRenameTests: XCTestCase {
    /// LocalVaultIO, recording which ciphertext it handed out whole and what it streamed.
    final class RecordingStreamIO: VaultFileSource, VaultFileSink, VaultFileStreamSource, VaultFileStreamSink,
                                   @unchecked Sendable {
        let base: LocalVaultIO
        var wholeReads: [String] = []
        var downloads: [String] = []
        var streamedCreates: [String] = []
        /// Entries another client wrote that this one has not heard of: absent from listings.
        var unseen: Set<String> = []
        init(root: URL) { base = LocalVaultIO(root: root) }
        func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
            try await base.listDir(relPath).filter { !unseen.contains(relPath + "/" + $0.name) }
        }
        func read(_ relPath: String) async throws -> Data { wholeReads.append(relPath); return try await base.read(relPath) }
        func makeDir(_ relPath: String) async throws { try await base.makeDir(relPath) }
        func writeFile(_ relPath: String, _ data: Data) async throws { try await base.writeFile(relPath, data) }
        func createFile(_ relPath: String, _ data: Data) async throws { try await base.createFile(relPath, data) }
        func remove(_ relPath: String) async throws { try await base.remove(relPath) }
        func download(_ relPath: String, to destination: URL) async throws {
            downloads.append(relPath); try await base.download(relPath, to: destination)
        }
        func createFile(_ relPath: String, contentsOf file: URL) async throws {
            streamedCreates.append(relPath); try await base.createFile(relPath, contentsOf: file)
        }
    }

    private var root: URL!
    override func setUp() {
        super.setUp()
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    }
    override func tearDown() { try? FileManager.default.removeItem(at: root); super.tearDown() }

    private let big = Data((0..<(3 * Vault.chunkPlainSize + 123)).map { UInt8(($0 * 7) & 0xff) })

    func testRenamingAMultiChunkFileStreamsIt() async throws {
        let io = RecordingStreamIO(root: root)
        let v = try await Vault.create(sink: io, password: "pw")
        try await v.addFile(name: "clip.mov", data: big, parentDirID: "", sink: io)
        let initial = try await v.listEntries(dirID: "", source: io)
        let entry = try XCTUnwrap(initial.first { $0.name == "clip.mov" })
        let oldContent = try XCTUnwrap(entry.contentPath)
        io.wholeReads = []

        try await v.renameEntry(entry, to: "renamed.mov", parentDirID: "", source: io, sink: io)

        XCTAssertFalse(io.wholeReads.contains(oldContent), "the contents must not be read into memory")
        XCTAssertEqual(io.downloads, [oldContent])
        XCTAssertEqual(io.streamedCreates, [v.entryPath(name: "renamed.mov", parentDirID: "")])
        let listed = try await v.listEntries(dirID: "", source: io)
        XCTAssertEqual(listed.map(\.name), ["renamed.mov"])
        let renamedBytes = try await v.decryptFile(at: try XCTUnwrap(listed[0].contentPath), source: io)
        XCTAssertEqual(renamedBytes, big)
    }

    func testMovingALongNamedMultiChunkFileStreamsIt() async throws {
        let io = RecordingStreamIO(root: root)
        let v = try await Vault.create(sink: io, password: "pw")
        let docs = try await v.createFolder(name: "docs", parentDirID: "", sink: io)
        try await v.addFile(name: "a.bin", data: big, parentDirID: "", sink: io)
        let initial = try await v.listEntries(dirID: "", source: io)
        let entry = try XCTUnwrap(initial.first { $0.name == "a.bin" })
        let long = String(repeating: "L", count: 200) + ".bin"   // past the shortening threshold

        try await v.moveEntry(entry, from: "", to: docs, as: long, source: io, sink: io)

        XCTAssertEqual(io.downloads.count, 1)
        XCTAssertEqual(io.streamedCreates, [v.entryPath(name: long, parentDirID: docs) + "/contents.c9r"])
        let rootNames = try await v.listEntries(dirID: "", source: io).map(\.name)
        XCTAssertEqual(rootNames, ["docs"])
        let moved = try await v.listEntries(dirID: docs, source: io)
        XCTAssertEqual(moved.map(\.name), [long])
        let movedBytes = try await v.decryptFile(at: try XCTUnwrap(moved[0].contentPath), source: io)
        XCTAssertEqual(movedBytes, big)
    }

    /// The destination is still create-only on the streaming path: a name taken meanwhile
    /// is refused, and the source stays where it was.
    func testStreamingRenameOntoATakenNameIsRefused() async throws {
        let io = RecordingStreamIO(root: root)
        let v = try await Vault.create(sink: io, password: "pw")
        try await v.addFile(name: "a.bin", data: big, parentDirID: "", sink: io)
        // Written behind the vault's back: the ciphertext file for "b.bin" already exists.
        let theirs = v.entryPath(name: "b.bin", parentDirID: "")
        try await io.base.writeFile(theirs, Data("theirs".utf8))
        io.unseen.insert(theirs)
        let initial = try await v.listEntries(dirID: "", source: io)
        let entry = try XCTUnwrap(initial.first { $0.name == "a.bin" })
        do {
            try await v.renameEntry(entry, to: "b.bin", parentDirID: "", source: io, sink: io)
            XCTFail("a taken name must be refused")
        } catch Vault.VaultError.nameTaken(let name) {
            XCTAssertEqual(name, "b.bin")
        }
        XCTAssertEqual(io.downloads.count, 1, "the refusal came from the streamed create itself")
        let kept = try await v.decryptFile(at: try XCTUnwrap(entry.contentPath), source: io)
        XCTAssertEqual(kept, big)
        let untouched = try await io.base.read(theirs)
        XCTAssertEqual(untouched, Data("theirs".utf8), "what took the name is untouched")
    }
}
