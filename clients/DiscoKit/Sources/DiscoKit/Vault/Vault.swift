import Foundation
import CryptoKit

// Cryptomator-compatible vault (format 8, SIV_GCM). Port of daemon/internal/vault.
// Immutable (keys only) → Sendable.
public final class Vault: Sendable {
    let encKey: [UInt8]   // 32B — AES-GCM content key + header key
    let macKey: [UInt8]   // 32B — SIV MAC half + JWT signature key

    init(encKey: [UInt8], macKey: [UInt8]) {
        self.encKey = encKey
        self.macKey = macKey
    }

    public enum VaultError: Error, Equatable {
        case wrongPassword, badMasterkey, badVaultFile, unsupported(String)
        /// A rename or move onto a name the directory already holds.
        case nameTaken(String)
        /// A decrypted name that is not a single path component ("", ".", "..", or one
        /// containing "/" or NUL). Anyone with the vault password can write one.
        case invalidName
        /// masterkey.cryptomator asks for scrypt parameters outside what a real vault uses:
        /// N not a power of two or above 2^20, r outside 1...32, or p below 1. Anyone who can
        /// write the vault folder writes this file; N = 0 would divide by zero and a huge
        /// N·r would try to allocate gigabytes.
        case invalidScryptParameters(n: Int, r: Int, p: Int)
    }

    /// Refuses scrypt parameters no Cryptomator vault uses (it writes N = 2^15, r = 8, p = 1).
    public static func validateScryptParameters(n: Int, r: Int, p: Int) throws {
        guard n >= 1, n <= 1 << 20, n & (n - 1) == 0, r >= 1, r <= 32, p >= 1 else {
            throw VaultError.invalidScryptParameters(n: n, r: r, p: p)
        }
    }

    /// The unwrapped keys as 64 bytes (encryption key, then MAC key): what the app hands to
    /// the File Provider extension through the keychain so it can serve an unlocked vault
    /// without ever seeing the password.
    public var rawKeys: Data { Data(encKey + macKey) }

    public init(rawKeys: Data) throws {
        guard rawKeys.count == 64 else { throw VaultError.badMasterkey }
        self.encKey = Array(rawKeys[rawKeys.startIndex..<rawKeys.startIndex + 32])
        self.macKey = Array(rawKeys[rawKeys.startIndex + 32..<rawKeys.startIndex + 64])
    }

    private struct MasterkeyFile: Decodable {
        let scryptSalt: String
        let scryptCostParam: Int
        let scryptBlockSize: Int
        let primaryMasterKey: String
        let hmacMasterKey: String
    }

    // Open a vault from a local directory using a password.
    public static func open(directory: URL, password: String) throws -> Vault {
        let mkData = try Data(contentsOf: directory.appendingPathComponent("masterkey.cryptomator"))
        let jwt = try String(contentsOf: directory.appendingPathComponent("vault.cryptomator"), encoding: .utf8)
        return try open(masterkeyData: mkData, vaultJWT: jwt, password: password)
    }

    // Open a vault via a source (server): downloads masterkey.cryptomator + vault.cryptomator.
    public static func open(source: VaultFileSource, password: String) async throws -> Vault {
        async let mkData = source.read("masterkey.cryptomator")
        async let jwtData = source.read("vault.cryptomator")
        return try await open(masterkeyData: mkData, vaultJWT: String(decoding: jwtData, as: UTF8.self),
                              password: password)
    }

    static func open(masterkeyData mkData: Data, vaultJWT: String, password: String) throws -> Vault {
        let mk = try JSONDecoder().decode(MasterkeyFile.self, from: mkData)
        guard let salt = Data(base64Encoded: mk.scryptSalt),
              let wrappedEnc = Data(base64Encoded: mk.primaryMasterKey),
              let wrappedMac = Data(base64Encoded: mk.hmacMasterKey) else { throw VaultError.badMasterkey }
        try validateScryptParameters(n: mk.scryptCostParam, r: mk.scryptBlockSize, p: 1)
        let kek = Scrypt.derive(password: Array(password.utf8), salt: Array(salt),
                                n: mk.scryptCostParam, r: mk.scryptBlockSize, p: 1, dkLen: 32)
        let encKey: [UInt8], macKey: [UInt8]
        do {
            encKey = try AESKeyWrap.unwrap(kek: kek, wrapped: Array(wrappedEnc))
            macKey = try AESKeyWrap.unwrap(kek: kek, wrapped: Array(wrappedMac))
        } catch {
            throw VaultError.wrongPassword
        }
        try verifyVaultJWT(vaultJWT.trimmingCharacters(in: .whitespacesAndNewlines), encKey: encKey, macKey: macKey)
        return Vault(encKey: encKey, macKey: macKey)
    }

    private struct JWTPayload: Decodable { let format: Int; let cipherCombo: String }

    static func verifyVaultJWT(_ token: String, encKey: [UInt8], macKey: [UInt8]) throws {
        let parts = token.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 3 else { throw VaultError.badVaultFile }
        let sigKey = SymmetricKey(data: Data(encKey + macKey))
        let expected = HMAC<SHA256>.authenticationCode(for: Data((parts[0] + "." + parts[1]).utf8), using: sigKey)
        guard let gotSig = base64URLDecode(parts[2]) else { throw VaultError.badVaultFile }
        guard Array(expected) == gotSig else { throw VaultError.wrongPassword }
        guard let payloadData = base64URLDecode(parts[1]),
              let payload = try? JSONDecoder().decode(JWTPayload.self, from: Data(payloadData)) else {
            throw VaultError.badVaultFile
        }
        guard payload.format == 8 else { throw VaultError.unsupported("format \(payload.format)") }
        guard payload.cipherCombo == "SIV_GCM" else { throw VaultError.unsupported(payload.cipherCombo) }
    }
}

// base64url → bytes (with or without padding).
func base64URLDecode(_ s: String) -> [UInt8]? {
    var str = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
    while str.count % 4 != 0 { str.append("=") }
    return Data(base64Encoded: str).map(Array.init)
}
