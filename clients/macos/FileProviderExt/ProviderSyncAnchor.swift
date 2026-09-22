import Foundation
import FileProvider
import DiscoKit

// iOS must re-enumerate items after removing vault actions from context menus. A remote cursor alone would hide those local changes.
enum ProviderSyncAnchor {
    private static let prefix = Data("ios-actions-3:".utf8)
    static func encode(_ cursor: Int64) -> Data {
        #if os(iOS)
        return prefix + SyncAnchorCodec.encode(cursor)
        #else
        return SyncAnchorCodec.encode(cursor)
        #endif
    }
    static func decode(_ raw: Data) throws -> Int64 {
        #if os(iOS)
        guard raw.starts(with: prefix), let cursor = SyncAnchorCodec.decode(Data(raw.dropFirst(prefix.count))) else {
            throw NSFileProviderError(.syncAnchorExpired)
        }
        return cursor
        #else
        return SyncAnchorCodec.decode(raw) ?? 0
        #endif
    }
}
