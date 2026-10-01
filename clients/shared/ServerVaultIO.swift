import Foundation
import DiscoKit

// VaultFileSource/Sink backed by the server: reads encrypted vault files through the
// index + APIClient, writes them via PUT/POST. vaultRoot is the server path of the
// vault folder (e.g. "/testvault").
struct ServerVaultIO: VaultFileSource, VaultFileSink, VaultFileStreamSource, VaultFileStreamSink {
    let vaultRoot: String
    let index: IndexStore
    let client: APIClient
    // Creating a vault: every file is new, and one that turns out to exist is somebody
    // else's — the write fails rather than replace it.
    var createOnly = false

    private func full(_ rel: String) -> String { rel.isEmpty ? vaultRoot : vaultRoot + "/" + rel }

    private var conflictCleanup: VaultConflictCleanup { VaultConflictCleanup(index: index) }

    func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
        try await conflictCleanup.retry { try await client.delete(nodeID: $0) }
        guard let node = try index.node(atPath: full(relPath)) else { return [] }
        return (try index.children(of: node.id)).map { ($0.name, $0.isDir) }
    }

    func read(_ relPath: String) async throws -> Data {
        guard let node = try index.node(atPath: full(relPath)) else {
            throw NSError(domain: "vault", code: 404, userInfo: [NSLocalizedDescriptionKey: "missing \(relPath)"])
        }
        return try await client.downloadData(nodeID: node.id)
    }

    // Create the whole directory chain (idempotent).
    func makeDir(_ relPath: String) async throws {
        var acc = ""
        for p in relPath.split(separator: "/").map(String.init) {
            acc = acc.isEmpty ? p : acc + "/" + p
            try? await client.createDir(relPath: full(acc))
        }
    }

    func writeFile(_ relPath: String, _ data: Data) async throws {
        if createOnly { return try await createFile(relPath, data) }
        let parent = (relPath as NSString).deletingLastPathComponent
        if !parent.isEmpty { try await makeDir(parent) }
        try await client.upload(data: data, relPath: full(relPath))
    }

    // Base version 0 matches no existing file: the server creates the file, or keeps the one
    // that is there and files ours beside it as a conflict copy. That copy is removed again
    // — in a vault's storage it is a name nobody can decrypt — and the write fails.
    func createFile(_ relPath: String, _ data: Data) async throws {
        let tmp = FileManager.default.temporaryDirectory.appendingPathComponent("ddk-vault-\(UUID().uuidString)")
        try data.write(to: tmp)
        defer { try? FileManager.default.removeItem(at: tmp) }
        try await createFile(relPath, contentsOf: tmp)
    }

    // The ciphertext of a renamed or moved file, straight from disk (VaultFileStreamSink).
    func createFile(_ relPath: String, contentsOf file: URL) async throws {
        let parent = (relPath as NSString).deletingLastPathComponent
        if !parent.isEmpty { try await makeDir(parent) }
        let outcome = try await client.upload(fileURL: file, relPath: full(relPath), modifiedAt: nil,
                                              baseVersion: ContentVersionCodec.unknownBase)
        guard outcome.conflicted else { return }
        if !outcome.nodeID.isEmpty {
            try index.queueVaultConflictRemoval(nodeID: outcome.nodeID)
            try await conflictCleanup.retry { try await client.delete(nodeID: $0) }
        }
        throw Vault.VaultError.nameTaken(full(relPath))
    }

    // A file's ciphertext to disk, never whole in memory (VaultFileStreamSource).
    func download(_ relPath: String, to destination: URL) async throws {
        guard let node = try index.node(atPath: full(relPath)) else { throw CocoaError(.fileNoSuchFile) }
        try await client.download(nodeID: node.id, to: destination)
    }

    func remove(_ relPath: String) async throws {
        guard let node = try index.node(atPath: full(relPath)) else { return }
        try await client.delete(nodeID: node.id)
    }
}

extension ServerVaultIO {
    func decryptFile(_ path: String, vault: Vault, to destination: URL) async throws {
        guard let node = try index.node(atPath: full(path)) else { throw CocoaError(.fileNoSuchFile) }
        let encrypted = FileManager.default.temporaryDirectory.appendingPathComponent("cipher-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: encrypted) }
        try await client.download(nodeID: node.id, to: encrypted)
        try vault.decryptContent(from: encrypted, to: destination)
    }
}

// File uploads keep both plaintext and ciphertext out of a whole-file Data allocation.
extension ServerVaultIO {
    func addFile(name: String, fileURL: URL, parentDirID: String, vault: Vault, createOnly: Bool = true) async throws {
        let entry = vault.entryPath(name: name, parentDirID: parentDirID)
        var path = entry
        if entry.hasSuffix(".c9s") {
            let metadata = Data(vault.encryptName(name, parentDirID: parentDirID).utf8)
            if createOnly { try await createFile(entry + "/name.c9s", metadata) }
            else { try await writeFile(entry + "/name.c9s", metadata) }
            path += "/contents.c9r"
        }
        try await uploadFile(fileURL, to: path, vault: vault, baseVersion: createOnly ? 0 : nil)
    }

    func uploadFile(_ file: URL, to path: String, vault: Vault, baseVersion: Int64?) async throws {
        let encrypted = FileManager.default.temporaryDirectory.appendingPathComponent("cipher-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: encrypted) }
        try vault.encryptContent(from: file, to: encrypted)
        let parent = (path as NSString).deletingLastPathComponent
        if !parent.isEmpty { try await makeDir(parent) }
        let outcome = try await client.upload(fileURL: encrypted, relPath: full(path), modifiedAt: nil, baseVersion: baseVersion)
        guard outcome.conflicted else { return }
        if !outcome.nodeID.isEmpty {
            try index.queueVaultConflictRemoval(nodeID: outcome.nodeID)
            try await conflictCleanup.retry { try await client.delete(nodeID: $0) }
        }
        throw Vault.VaultError.nameTaken(path)
    }
}
