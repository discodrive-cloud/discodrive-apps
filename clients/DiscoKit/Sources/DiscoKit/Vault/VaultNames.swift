import Foundation
import CryptoKit

// Name encryption and DirIdHash (Cryptomator fmt8). Port of name.go.
extension Vault {
    // Encrypt a name → "<base64url>.c9r". AAD = parentDirID.
    public func encryptName(_ name: String, parentDirID: String) -> String {
        let ct = AESSIV.encrypt(macKey: macKey, encKey: encKey,
                                plaintext: Array(name.utf8), aad: Array(parentDirID.utf8))
        return base64URLEncodePadded(ct) + ".c9r"
    }

    // Decrypt a .c9r name.
    public func decryptName(_ encNameC9r: String, parentDirID: String) throws -> String {
        guard encNameC9r.hasSuffix(".c9r") else { throw VaultError.badVaultFile }
        let encName = String(encNameC9r.dropLast(4))
        guard let ct = base64URLDecode(encName) else { throw VaultError.badVaultFile }
        let pt = try AESSIV.decrypt(macKey: macKey, encKey: encKey, ciphertext: ct, aad: Array(parentDirID.utf8))
        let name = String(decoding: pt, as: UTF8.self)
        guard Vault.isValidPlainName(name) else { throw VaultError.invalidName }
        return name
    }

    /// A plaintext name must stay one path component wherever it is joined into a URL.
    public static func isValidPlainName(_ name: String) -> Bool {
        !(name.isEmpty || name == "." || name == ".." || name.contains("/") || name.contains("\u{0}"))
    }

    // .c9s name for a long encrypted name: base64url(sha1(fullEncName)) + ".c9s".
    func shortenedName(_ fullEncName: String) -> String {
        let h = Insecure.SHA1.hash(data: Data(fullEncName.utf8))
        return base64URLEncodeRaw(Array(h)) + ".c9s"
    }

    // Storage path for a directory by its ID: SIV(no AAD) → SHA1 → base32 → "d/XX/YYY".
    public func dirIdHash(_ dirID: String) -> String {
        let ct = AESSIV.encrypt(macKey: macKey, encKey: encKey, plaintext: Array(dirID.utf8), aad: nil)
        let sha = Insecure.SHA1.hash(data: Data(ct))
        let b32 = base32Encode(Array(sha))
        return "d/" + String(b32.prefix(2)) + "/" + String(b32.dropFirst(2))
    }
}

// base64url with padding (matches Go's base64.URLEncoding).
func base64URLEncodePadded(_ bytes: [UInt8]) -> String {
    Data(bytes).base64EncodedString()
        .replacingOccurrences(of: "+", with: "-")
        .replacingOccurrences(of: "/", with: "_")
}

// base64url without padding (RawURLEncoding).
func base64URLEncodeRaw(_ bytes: [UInt8]) -> String {
    base64URLEncodePadded(bytes).replacingOccurrences(of: "=", with: "")
}

// base32 RFC 4648 (A-Z2-7), uppercase, no padding.
func base32Encode(_ data: [UInt8]) -> String {
    let alphabet = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567")
    var out = ""
    var buffer = 0, bits = 0
    for byte in data {
        buffer = (buffer << 8) | Int(byte)
        bits += 8
        while bits >= 5 {
            bits -= 5
            out.append(alphabet[(buffer >> bits) & 0x1f])
        }
    }
    if bits > 0 { out.append(alphabet[(buffer << (5 - bits)) & 0x1f]) }
    return out
}
