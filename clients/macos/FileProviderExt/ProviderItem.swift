import FileProvider
import UniformTypeIdentifiers
import DiscoKit

// An index node as Finder sees it. Read-only for now: writing lands in the next phase.
final class ProviderItem: NSObject, NSFileProviderItem {
    let info: ProviderItemInfo

    init(node: Node) { info = ProviderItemInfo(node: node) }

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
        info.isDirectory ? [.allowsReading, .allowsContentEnumerating] : [.allowsReading]
    }
    // Files are placeholders until opened; the system downloads on demand and may evict.
    var contentPolicy: NSFileProviderContentPolicy { info.isDirectory ? .inherited : .downloadLazily }
    var documentSize: NSNumber? { info.isDirectory ? nil : NSNumber(value: info.size) }
    var itemVersion: NSFileProviderItemVersion {
        .init(contentVersion: info.contentVersion, metadataVersion: info.metadataVersion)
    }
}

// The domain root: the "DiscoDrive" folder itself.
final class RootItem: NSObject, NSFileProviderItem {
    var itemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { "DiscoDrive" }
    var contentType: UTType { .folder }
    var capabilities: NSFileProviderItemCapabilities { [.allowsReading, .allowsContentEnumerating] }
    var itemVersion: NSFileProviderItemVersion {
        .init(contentVersion: Data("root".utf8), metadataVersion: Data("root".utf8))
    }
}
