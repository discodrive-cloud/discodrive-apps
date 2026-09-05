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
