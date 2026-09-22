import Foundation
import CryptoKit

extension Vault {
    // File-to-file variants keep memory bounded by a 32 KiB Cryptomator frame.
    // The destination is published only after every authenticated frame succeeds.
    public func encryptContent(from source: URL, to destination: URL) throws {
        try streamFile(source, to: destination) { input, output in
            let nonce = AES.GCM.Nonce()
            let key = SymmetricKey(data: Data(Vault.randomBytes(32)))
            let payload = Data(repeating: 0xff, count: 8) + key.withUnsafeBytes { Data($0) }
            let header = try AES.GCM.seal(payload, using: SymmetricKey(data: Data(encKey)), nonce: nonce)
            try output.write(contentsOf: header.combined!)
            var index: UInt64 = 0
            while true {
                try Task.checkCancellation()
                let chunk = try readBlock(input, count: Vault.chunkPlainSize)
                if chunk.isEmpty { break }
                var aad = Data(beBytes(index)); aad.append(Data(nonce))
                let box = try AES.GCM.seal(chunk, using: key, nonce: AES.GCM.Nonce(), authenticating: aad)
                try output.write(contentsOf: box.combined!)
                index += 1
            }
        }
    }

    public func decryptContent(from source: URL, to destination: URL) throws {
        try streamFile(source, to: destination) { input, output in
            let header = try readBlock(input, count: Vault.headerTotalSize)
            guard header.count == Vault.headerTotalSize else { throw VaultError.badVaultFile }
            let payload = try AES.GCM.open(AES.GCM.SealedBox(combined: header), using: SymmetricKey(data: Data(encKey)))
            guard payload.count == 40 else { throw VaultError.badVaultFile }
            let key = SymmetricKey(data: payload.subdata(in: 8..<40))
            let nonce = header.prefix(12)
            var index: UInt64 = 0
            while true {
                try Task.checkCancellation()
                let frame = try readBlock(input, count: 12 + Vault.chunkPlainSize + 16)
                if frame.isEmpty { break }
                var aad = Data(beBytes(index)); aad.append(nonce)
                let plain = try AES.GCM.open(AES.GCM.SealedBox(combined: frame), using: key, authenticating: aad)
                try output.write(contentsOf: plain)
                index += 1
            }
        }
    }
}

private func readBlock(_ input: FileHandle, count: Int) throws -> Data {
    var result = Data()
    while result.count < count {
        guard let chunk = try input.read(upToCount: count - result.count), !chunk.isEmpty else { break }
        result.append(chunk)
    }
    return result
}

private func streamFile(_ source: URL, to destination: URL, body: (FileHandle, FileHandle) throws -> Void) throws {
    let temporary = destination.deletingLastPathComponent().appendingPathComponent(".vault-\(UUID().uuidString)")
    let fm = FileManager.default
    guard fm.createFile(atPath: temporary.path, contents: nil, attributes: [.posixPermissions: 0o600]) else { throw CocoaError(.fileWriteUnknown) }
    defer { try? fm.removeItem(at: temporary) }
    let input = try FileHandle(forReadingFrom: source)
    defer { try? input.close() }
    let output = try FileHandle(forWritingTo: temporary)
    do { try body(input, output); try output.close() }
    catch { try? output.close(); throw error }
    try Task.checkCancellation()
    try fm.moveItem(at: temporary, to: destination)
}
