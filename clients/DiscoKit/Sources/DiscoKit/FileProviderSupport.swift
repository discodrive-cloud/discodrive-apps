import Foundation

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
    // Changes when the bytes change → the system re-downloads a materialised file.
    public let contentVersion: Data
    // Changes when name / size / version change → the system re-reads metadata.
    public let metadataVersion: Data

    public init(node: Node) {
        identifier = node.id
        parentIdentifier = node.parentID ?? Self.rootIdentifier
        filename = node.name
        isDirectory = node.isDir
        size = node.size
        // A directory has no content hash; "dir" keeps the version non-empty, which the
        // framework requires.
        contentVersion = Data((node.isDir ? "dir" : node.contentHash).utf8)
        metadataVersion = Data("\(node.version):\(node.name):\(node.size)".utf8)
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
            let rest = raw.dropFirst(5)
            guard let colon = rest.firstIndex(of: ":") else { return nil }
            return .file(parentDirID: String(rest[rest.startIndex..<colon]), nodeID: String(rest[rest.index(after: colon)...]))
        }
        return nil
    }
}
