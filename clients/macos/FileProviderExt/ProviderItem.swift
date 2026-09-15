import FileProvider
import UniformTypeIdentifiers
import DiscoKit

// An index node as Finder sees it. Writable unless it belongs to a Cryptomator vault:
// the ciphertext layout is the vault's, and a stray write from Finder would corrupt it.
final class ProviderItem: NSObject, NSFileProviderItem {
    let info: ProviderItemInfo
    let writable: Bool
    let vaultRoot: Bool

    init(node: Node, writable: Bool = true, vaultRoot: Bool = false) {
        info = ProviderItemInfo(node: node)
        self.writable = writable
        self.vaultRoot = vaultRoot
    }

    // Read by the action rules in Info.plist: "Open vault" shows on a vault's folder.
    var userInfo: [AnyHashable: Any]? { vaultRoot ? ["vault": 1] : nil }

    var itemIdentifier: NSFileProviderItemIdentifier { .init(info.identifier) }
    var parentItemIdentifier: NSFileProviderItemIdentifier {
        info.parentIdentifier == ProviderItemInfo.rootIdentifier ? .rootContainer : .init(info.parentIdentifier)
    }
    var filename: String { info.filename }
    var contentType: UTType {
        if info.isDirectory { return .folder }
        let ext = (info.filename as NSString).pathExtension
        return ext.isEmpty ? .data : (UTType(filenameExtension: ext) ?? .data)
    }
    var capabilities: NSFileProviderItemCapabilities {
        var caps: NSFileProviderItemCapabilities = info.isDirectory ? [.allowsReading, .allowsContentEnumerating] : [.allowsReading]
        if writable {
            caps.formUnion([.allowsRenaming, .allowsReparenting, .allowsDeleting])
            caps.formUnion(info.isDirectory ? [.allowsAddingSubItems] : [.allowsWriting])
        }
        return caps
    }
    // Files are placeholders until opened; the system downloads on demand and may evict.
    var contentPolicy: NSFileProviderContentPolicy { info.isDirectory ? .inherited : .downloadLazily }
    var documentSize: NSNumber? { info.isDirectory ? nil : NSNumber(value: info.size) }
    // Whether a folder is a vault root is metadata too: Finder re-reads the item, and so
    // the context-menu actions, when the flag flips.
    var itemVersion: NSFileProviderItemVersion {
        .init(contentVersion: info.contentVersion, metadataVersion: info.metadataVersion + Data((vaultRoot ? ":vault" : "").utf8))
    }
}

// The domain root: the "DiscoDrive" folder itself.
final class RootItem: NSObject, NSFileProviderItem {
    var itemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { "DiscoDrive" }
    var contentType: UTType { .folder }
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading, .allowsContentEnumerating, .allowsAddingSubItems] }
    var itemVersion: NSFileProviderItemVersion {
        .init(contentVersion: Data("root".utf8), metadataVersion: Data("root".utf8))
    }
}
