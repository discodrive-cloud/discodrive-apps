import Foundation

// Source of encrypted vault files (relPath relative to the vault root). Async — suitable for server use.
public protocol VaultFileSource: Sendable {
    func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)]
    func read(_ relPath: String) async throws -> Data
}

// Sink for writing to a vault (create / upload / delete).
public protocol VaultFileSink: Sendable {
    func makeDir(_ relPath: String) async throws
    func writeFile(_ relPath: String, _ data: Data) async throws
    /// Writes a file that must not be there yet, and throws `Vault.VaultError.nameTaken`
    /// without touching what is when it is. The check and the write are one step on the
    /// sink's side: a name looked up first and written after can be taken in between.
    func createFile(_ relPath: String, _ data: Data) async throws
    func remove(_ relPath: String) async throws
}

// A decrypted entry inside the vault (plaintext name + location of its content or subdirectory).
public struct VaultEntry: Sendable {
    public let name: String
    public let isDir: Bool
    public let dirID: String?       // for a directory — the subdirectory ID
    public let contentPath: String? // for a file — relPath of the encrypted content blob
    public let encPath: String      // relPath of the encrypted entry itself (.c9r/.c9s) in storage

    // The ciphertext node that carries this entry's size and version. For a file it is the
    // content blob: under a long name that is contents.c9r inside the .c9s wrapper, and the
    // wrapper folder itself has no size and does not change when the contents do.
    public var nodePath: String { isDir ? encPath : (contentPath ?? encPath) }
}

let shorteningThreshold = 220

extension Vault {
    // Which of the known directories a batch of changed ciphertext paths (relative to the
    // vault root) touches. A directory's storage folder is dirIdHash(id) — "d/XX/YYY…" —
    // and an entry changed there is anything beneath it.
    public func dirIDs(touchedBy relPaths: [String], knownDirIDs: [String]) -> Set<String> {
        var byStorage: [String: String] = [dirIdHash(""): ""]
        for id in knownDirIDs { byStorage[dirIdHash(id)] = id }
        var out = Set<String>()
        for rel in relPaths {
            for (storage, dirID) in byStorage where rel.hasPrefix(storage + "/") { out.insert(dirID) }
        }
        return out
    }

    // List decrypted entries of a directory. Port of decrypt.go (decryptDir).
    public func listEntries(dirID: String, source: VaultFileSource) async throws -> [VaultEntry] {
        let storage = dirIdHash(dirID)
        var out = [VaultEntry]()
        for e in try await source.listDir(storage) {
            if e.name == "dirid.c9r" { continue }
            // A server conflict name is not an encrypted entry. Keep the rest of the
            // directory readable while its queued removal is retried, even on another device.
            if e.name.contains(" (conflict, ") { continue }
            let entryPath = storage + "/" + e.name

            if e.name.hasSuffix(".c9s") && e.isDir {
                let fullEnc = String(decoding: try await source.read(entryPath + "/name.c9s"), as: UTF8.self)
                let plain = try decryptName(fullEnc, parentDirID: dirID)
                let children = (try? await source.listDir(entryPath)) ?? []
                if children.contains(where: { $0.name == "dir.c9r" }) {
                    let subID = String(decoding: try await source.read(entryPath + "/dir.c9r"), as: UTF8.self)
                    out.append(VaultEntry(name: plain, isDir: true, dirID: subID, contentPath: nil, encPath: entryPath))
                } else if children.contains(where: { $0.name == "contents.c9r" }) {
                    out.append(VaultEntry(name: plain, isDir: false, dirID: nil, contentPath: entryPath + "/contents.c9r", encPath: entryPath))
                }
                continue
            }

            guard e.name.hasSuffix(".c9r") else { continue }
            let plain = try decryptName(e.name, parentDirID: dirID)
            if e.isDir {
                let subID = String(decoding: try await source.read(entryPath + "/dir.c9r"), as: UTF8.self)
                out.append(VaultEntry(name: plain, isDir: true, dirID: subID, contentPath: nil, encPath: entryPath))
            } else {
                out.append(VaultEntry(name: plain, isDir: false, dirID: nil, contentPath: entryPath, encPath: entryPath))
            }
        }
        return out
    }

    public func decryptFile(at contentPath: String, source: VaultFileSource) async throws -> Data {
        try decryptContent(await source.read(contentPath))
    }
}

// Local FileManager-based source/sink (for tests and on-disk vaults).
public struct LocalVaultIO: VaultFileSource, VaultFileSink {
    let root: URL
    public init(root: URL) { self.root = root }

    public func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
        let dir = root.appendingPathComponent(relPath)
        let items = try FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.isDirectoryKey])
        return items.map { url in
            let isDir = (try? url.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory ?? false
            return (url.lastPathComponent, isDir)
        }
    }
    public func read(_ relPath: String) async throws -> Data {
        try Data(contentsOf: root.appendingPathComponent(relPath))
    }
    public func makeDir(_ relPath: String) async throws {
        try FileManager.default.createDirectory(at: root.appendingPathComponent(relPath),
                                                withIntermediateDirectories: true)
    }
    public func writeFile(_ relPath: String, _ data: Data) async throws {
        let url = root.appendingPathComponent(relPath)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try data.write(to: url)
    }
    public func createFile(_ relPath: String, _ data: Data) async throws {
        let url = root.appendingPathComponent(relPath)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        do { try data.write(to: url, options: .withoutOverwriting) }
        catch CocoaError.fileWriteFileExists { throw Vault.VaultError.nameTaken(relPath) }
    }
    public func remove(_ relPath: String) async throws {
        try FileManager.default.removeItem(at: root.appendingPathComponent(relPath))
    }
}
