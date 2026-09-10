import Foundation
import DiscoKit

// VaultFileSource/Sink backed by the server: reads encrypted vault files through the
// index + APIClient, writes them via PUT/POST. vaultRoot is the server path of the
// vault folder (e.g. "/testvault").
struct ServerVaultIO: VaultFileSource, VaultFileSink {
    let vaultRoot: String
    let index: IndexStore
    let client: APIClient

    private func full(_ rel: String) -> String { rel.isEmpty ? vaultRoot : vaultRoot + "/" + rel }

    func listDir(_ relPath: String) async throws -> [(name: String, isDir: Bool)] {
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
        let parent = (relPath as NSString).deletingLastPathComponent
        if !parent.isEmpty { try await makeDir(parent) }
        try await client.upload(data: data, relPath: full(relPath))
    }

    func remove(_ relPath: String) async throws {
        guard let node = try index.node(atPath: full(relPath)) else { return }
        try await client.delete(nodeID: node.id)
    }
}
