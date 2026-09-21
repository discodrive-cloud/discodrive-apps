import Foundation
import CryptoKit

// Vault write operations: create vault, upload files, create folders. Port of vault.go Create + encrypt.go.
extension Vault {
    // Create a new Cryptomator vault via a sink. Returns the opened Vault.
    public static func create(sink: VaultFileSink, password: String) async throws -> Vault {
        let encKey = randomBytes(32)
        let macKey = randomBytes(32)
        let salt = randomBytes(8)
        let kek = Scrypt.derive(password: Array(password.utf8), salt: salt, n: 32768, r: 8, p: 1, dkLen: 32)
        let wrappedEnc = try AESKeyWrap.wrap(kek: kek, plaintext: encKey)
        let wrappedMac = try AESKeyWrap.wrap(kek: kek, plaintext: macKey)
        let versionMac = Array(HMAC<SHA256>.authenticationCode(
            for: Data([0x00, 0x00, 0x03, 0xe7]),  // BE32(999)
            using: SymmetricKey(data: Data(macKey))))

        func b64(_ b: [UInt8]) -> String { Data(b).base64EncodedString() }
        let mkJSON = """
        {
          "version": 999,
          "scryptSalt": "\(b64(salt))",
          "scryptCostParam": 32768,
          "scryptBlockSize": 8,
          "primaryMasterKey": "\(b64(wrappedEnc))",
          "hmacMasterKey": "\(b64(wrappedMac))",
          "versionMac": "\(b64(versionMac))"
        }
        """

        let header = #"{"kid":"masterkeyfile:masterkey.cryptomator","alg":"HS256","typ":"JWT"}"#
        let payload = "{\"jti\":\"\(UUID().uuidString.lowercased())\",\"format\":8,\"cipherCombo\":\"SIV_GCM\",\"shorteningThreshold\":220}"
        let hp = base64URLEncodeRaw(Array(header.utf8))
        let pp = base64URLEncodeRaw(Array(payload.utf8))
        let sig = HMAC<SHA256>.authenticationCode(for: Data((hp + "." + pp).utf8),
                                                  using: SymmetricKey(data: Data(encKey + macKey)))
        let jwt = hp + "." + pp + "." + base64URLEncodeRaw(Array(sig))

        let vault = Vault(encKey: encKey, macKey: macKey)
        try await sink.writeFile("masterkey.cryptomator", Data(mkJSON.utf8))
        try await sink.writeFile("vault.cryptomator", Data(jwt.utf8))

        // Root directory: dirIdHash("") / dirid.c9r = EncryptContent("").
        let rootStorage = vault.dirIdHash("")
        try await sink.makeDir(rootStorage)
        try await sink.writeFile(rootStorage + "/dirid.c9r", try vault.encryptContent(Data()))
        return vault
    }

    // Where addFile/createFolder put an entry of this name: its ciphertext path relative
    // to the vault root, so a writer can look the entry up after the write.
    public func entryPath(name: String, parentDirID: String) -> String {
        let encName = encryptName(name, parentDirID: parentDirID)
        return dirIdHash(parentDirID) + "/" + (encName.count > shorteningThreshold ? shortenedName(encName) : encName)
    }

    // Add a file to a vault directory (parentDirID, "" = root).
    //
    // `createOnly`: the name must be free — a new file, or the destination of a rename or
    // move. The contents are then written as "create, or fail": looking the name up first
    // is not enough, another client can take it between the look and the write.
    public func addFile(name: String, data: Data, parentDirID: String, sink: VaultFileSink, createOnly: Bool = false) async throws {
        let storage = dirIdHash(parentDirID)
        let encName = encryptName(name, parentDirID: parentDirID)
        let content = try encryptContent(data)
        let path: String
        if encName.count > shorteningThreshold {
            let base = storage + "/" + shortenedName(encName)
            // Both entry types claim this same file before writing their different payloads.
            try await sink.makeDir(base)
            if createOnly {
                try await creating(name) { try await sink.createFile(base + "/name.c9s", Data(encName.utf8)) }
            } else {
                try await sink.writeFile(base + "/name.c9s", Data(encName.utf8))
            }
            path = base + "/contents.c9r"
        } else {
            path = storage + "/" + encName
        }
        guard createOnly else { try await sink.writeFile(path, content); return }
        try await creating(name) { try await sink.createFile(path, content) }
    }

    // The sink names the ciphertext path that was taken; callers know the entry by its name.
    private func creating(_ name: String, _ write: () async throws -> Void) async throws {
        do { try await write() } catch VaultError.nameTaken { throw VaultError.nameTaken(name) }
    }

    // Create a subdirectory and return its dirID.
    @discardableResult
    public func createFolder(name: String, parentDirID: String, sink: VaultFileSink) async throws -> String {
        let storage = dirIdHash(parentDirID)
        let encName = encryptName(name, parentDirID: parentDirID)
        let subDirID = UUID().uuidString.lowercased()
        let base: String
        if encName.count > shorteningThreshold {
            base = storage + "/" + shortenedName(encName)
            try await sink.makeDir(base)
            try await creating(name) { try await sink.createFile(base + "/name.c9s", Data(encName.utf8)) }
        } else {
            base = storage + "/" + encName
            try await sink.makeDir(base)
        }
        // A folder that appeared under this name meanwhile keeps its directory id.
        try await creating(name) { try await sink.createFile(base + "/dir.c9r", Data(subDirID.utf8)) }
        // Storage location for the new directory.
        let subStorage = dirIdHash(subDirID)
        try await sink.makeDir(subStorage)
        try await sink.writeFile(subStorage + "/dirid.c9r", try encryptContent(Data(subDirID.utf8)))
        return subDirID
    }

    // Delete an entry (file — .c9r/.c9s; directory — recursively including all subtree storage).
    public func deleteEntry(_ entry: VaultEntry, source: VaultFileSource, sink: VaultFileSink) async throws {
        if entry.isDir, let dirID = entry.dirID {
            try await deleteSubtreeStorage(dirID: dirID, source: source, sink: sink)
        }
        try await sink.remove(entry.encPath)
    }

    private func deleteSubtreeStorage(dirID: String, source: VaultFileSource, sink: VaultFileSink) async throws {
        for child in try await listEntries(dirID: dirID, source: source) where child.isDir {
            if let sub = child.dirID { try await deleteSubtreeStorage(dirID: sub, source: source, sink: sink) }
        }
        try? await sink.remove(dirIdHash(dirID))   // storage for this directory and all its files
    }

    // Move an entry into another directory, optionally under a new name. Cryptomator names
    // are encrypted with the parent's directory id, so a file is re-encrypted under the new
    // parent and a directory gets a fresh wrapper there; the subtree's storage never moves.
    public func moveEntry(_ entry: VaultEntry, from parentDirID: String, to newParentDirID: String,
                          as newName: String? = nil, source: VaultFileSource, sink: VaultFileSink) async throws {
        let name = newName ?? entry.name
        guard !name.isEmpty else { return }
        if newParentDirID == parentDirID {
            try await renameEntry(entry, to: name, parentDirID: parentDirID, source: source, sink: sink)
            return
        }
        try await refuseTakenName(name, in: newParentDirID, source: source)
        if entry.isDir, let subDirID = entry.dirID {
            try await writeDirWrapper(name: name, subDirID: subDirID, parentDirID: newParentDirID, sink: sink)
        } else if let cp = entry.contentPath {
            let data = try await decryptFile(at: cp, source: source)
            try await addFile(name: name, data: data, parentDirID: newParentDirID, sink: sink, createOnly: true)
        } else {
            return
        }
        // Only now, with the destination written and known to be ours.
        try await sink.remove(entry.encPath)
    }

    // An entry is written by name, over whatever bears it: a file's contents would be
    // replaced, and a folder's dir.c9r pointed at another directory id — everything under
    // the old one still stored, and out of reach. A taken name is refused instead.
    private func refuseTakenName(_ name: String, in dirID: String, source: VaultFileSource) async throws {
        if try await listEntries(dirID: dirID, source: source).contains(where: { $0.name == name }) {
            throw VaultError.nameTaken(name)
        }
    }

    // The .c9r (or .c9s) wrapper that points a name in `parentDirID` at an existing subdirectory.
    private func writeDirWrapper(name: String, subDirID: String, parentDirID: String, sink: VaultFileSink) async throws {
        let storage = dirIdHash(parentDirID)
        let enc = encryptName(name, parentDirID: parentDirID)
        let base: String
        if enc.count > shorteningThreshold {
            base = storage + "/" + shortenedName(enc)
            try await sink.makeDir(base)
            try await creating(name) { try await sink.createFile(base + "/name.c9s", Data(enc.utf8)) }
        } else {
            base = storage + "/" + enc
            try await sink.makeDir(base)
        }
        // Over an existing dir.c9r this would point the name at the moved directory and
        // leave everything under the one it named out of reach.
        try await creating(name) { try await sink.createFile(base + "/dir.c9r", Data(subDirID.utf8)) }
    }

    // Rename an entry within the same directory.
    public func renameEntry(_ entry: VaultEntry, to newName: String, parentDirID: String,
                            source: VaultFileSource, sink: VaultFileSink) async throws {
        guard !newName.isEmpty, newName != entry.name else { return }
        try await refuseTakenName(newName, in: parentDirID, source: source)
        if entry.isDir, let subDirID = entry.dirID {
            try await writeDirWrapper(name: newName, subDirID: subDirID, parentDirID: parentDirID, sink: sink)
            try await sink.remove(entry.encPath)
        } else if let cp = entry.contentPath {
            // File: re-encrypt under the new name, then remove the old entry.
            let data = try await decryptFile(at: cp, source: source)
            try await addFile(name: newName, data: data, parentDirID: parentDirID, sink: sink, createOnly: true)
            try await sink.remove(entry.encPath)
        }
    }
}
