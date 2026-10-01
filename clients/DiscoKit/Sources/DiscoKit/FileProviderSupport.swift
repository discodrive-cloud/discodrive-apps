import Foundation
import CryptoKit

// The pure half of the File Provider extension: what an index node looks like as a
// provider item, and how the sync anchor is encoded. No FileProvider import here, so it is
// testable with `swift test`; the extension wraps these in NSFileProviderItem.
public struct ProviderItemInfo: Equatable, Sendable {
    // NSFileProviderItemIdentifier.rootContainer.rawValue — spelled out to stay framework-free.
    public static let rootIdentifier = "NSFileProviderRootContainerItemIdentifier"

    public let identifier: String
    public let parentIdentifier: String
    public let filename: String
    public let isDirectory: Bool
    public let size: Int64
    // Names the bytes AND the server version they belong to: the system hands it back as
    // the base of an edit, and that base is what guards the upload. It therefore also
    // changes when only the version does (a rename or move bumps it on the server), and
    // the system may fetch a materialised file again for the same bytes.
    public let contentVersion: Data
    // Changes when name / size / version change → the system re-reads metadata.
    public let metadataVersion: Data

    /// `unverifiedBytes`: the hash of downloaded bytes that match no version the index
    /// knows. They are served under a version-less contentVersion, so an edit of them is
    /// guarded as an edit of unknown base rather than credited to a version they are not.
    public init(node: Node, unverifiedBytes: String? = nil) {
        identifier = node.id
        parentIdentifier = node.parentID ?? Self.rootIdentifier
        filename = node.name
        isDirectory = node.isDir
        size = node.size
        // A directory has no content hash; "dir" keeps the version non-empty, which the
        // framework requires.
        if node.isDir { contentVersion = Data("dir".utf8) }
        else if let unverifiedBytes { contentVersion = Data(unverifiedBytes.utf8) }
        else { contentVersion = ContentVersionCodec.encode(version: node.version, hash: node.contentHash) }
        metadataVersion = Data("\(node.version):\(node.name):\(node.size)".utf8)
    }
}

// A file's contentVersion: "v1:<server version>:<content hash>". Versions handed out before
// this format were the bare hash; those decode with no version.
public enum ContentVersionCodec {
    public struct Base: Equatable, Sendable {
        public let version: Int64?   // nil: an old-format value, the version is not known
        public let hash: String
    }

    public static func encode(version: Int64, hash: String) -> Data { Data("v1:\(version):\(hash)".utf8) }

    public static func decode(_ data: Data) -> Base? {
        guard let text = String(data: data, encoding: .utf8), !text.isEmpty else { return nil }
        guard text.hasPrefix("v1:") else { return Base(version: nil, hash: text) }
        let parts = text.dropFirst(3).split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
        guard parts.count == 2, let version = Int64(parts[0]), version > 0 else { return nil }
        return Base(version: version, hash: String(parts[1]))
    }

    /// The `base_version` that guards an upload of contents the system says were edited
    /// from `base`, given what the index holds for the file now.
    ///
    /// Never the index's current version merely because it is current, and never "no
    /// base": either would let an edit of v1 land over a v2 the editor never saw. The
    /// server's versions start at 1, so `unknownBase` matches no existing file — the
    /// server keeps its own and files the edit beside it as a conflict copy, which is
    /// how an edit whose base cannot be told is kept.
    ///
    /// The one case the current version is right: the bytes that were edited are
    /// byte-for-byte what the server holds now (equal hashes), so nothing unseen can be
    /// overwritten. That covers a version bumped by a rename alone, and old-format
    /// values of files that have not changed since.
    public static func uploadBase(editedFrom base: Data, current: Node) -> Int64 {
        guard let decoded = decode(base) else { return unknownBase }
        if !decoded.hash.isEmpty, decoded.hash == current.contentHash { return current.version }
        return decoded.version ?? unknownBase
    }

    public static let unknownBase: Int64 = 0
}

// The server's content hash of a file on disk (hex SHA-256), read in pieces: a download is
// checked against the version it is about to be reported as.
public enum ContentHash {
    public static func sha256Hex(of url: URL) throws -> String {
        let handle = try FileHandle(forReadingFrom: url)
        defer { try? handle.close() }
        var hasher = SHA256()
        while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty { hasher.update(data: chunk) }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    /// Which of the nodes the downloaded bytes belong to: the one the index held before
    /// the download or the one it holds after a pull, whichever hash they match. Nil when
    /// they match neither — the server moved on mid-download; fetch again. A node with no
    /// hash cannot be checked: the bytes are then reported under the older of the two, so
    /// a wrong guess costs a conflict copy rather than an overwrite.
    public static func owner(ofDownloaded hash: String, before: Node, after: Node?) -> Node? {
        if !before.contentHash.isEmpty, before.contentHash == hash { return before }
        if let after, !after.contentHash.isEmpty, after.contentHash == hash { return after }
        if before.contentHash.isEmpty { return before }
        return nil
    }
}

// The anchor the system hands back to enumerateChanges(from:) is the index cursor —
// the last /sync/changes sequence number applied — as decimal text.
public enum SyncAnchorCodec {
    public static func encode(_ cursor: Int64) -> Data { Data(String(cursor).utf8) }
    public static func decode(_ data: Data) -> Int64? {
        String(data: data, encoding: .utf8).flatMap(Int64.init)
    }
}

// Names macOS invents for its own housekeeping — Finder's folder-icon file, .DS_Store,
// AppleDouble forks — must never reach the server: they mean nothing anywhere else and
// the daemon skips them on the way up too.
public enum LocalOnlyNames {
    public static func isLocalOnly(_ name: String) -> Bool {
        name == ".DS_Store" || name == "Icon\r" || name.hasPrefix("._") || name == ".localized"
    }
}

/// How an unlocked vault's entries are named as File Provider items. Cryptomator keeps no
/// back-links, so a file's identifier carries the directory id it lives in (needed to
/// decrypt its name) next to the id of its ciphertext node; a directory is its own
/// Cryptomator directory id, and the vault root is the root container.
public enum VaultItemID: Equatable, Sendable {
    case root
    case dir(dirID: String)
    case file(parentDirID: String, nodeID: String)

    public static func encode(_ id: VaultItemID) -> String {
        switch id {
        case .root: return ProviderItemInfo.rootIdentifier
        case .dir(let d): return "dir:" + d
        case .file(let p, let n): return "file:" + p + ":" + n
        }
    }

    public static func decode(_ raw: String) -> VaultItemID? {
        if raw == ProviderItemInfo.rootIdentifier { return .root }
        if raw.hasPrefix("dir:") { return .dir(dirID: String(raw.dropFirst(4))) }
        if raw.hasPrefix("file:") {
            // Split at the LAST colon: the node id is a server UUID, while the directory id
            // is whatever the vault's dir.c9r holds and may itself contain colons.
            let rest = raw.dropFirst(5)
            guard let colon = rest.lastIndex(of: ":") else { return nil }
            return .file(parentDirID: String(rest[rest.startIndex..<colon]), nodeID: String(rest[rest.index(after: colon)...]))
        }
        return nil
    }
}
