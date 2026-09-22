import Foundation

// Only a successfully prepared pairing may reuse Documents/Sync. This marker is
// independent of the sync database: an old database must never authorize old bytes.
enum FullSyncPairing {
    private static let key = "fullSync.preparedPairing"

    static func invalidate(defaults: UserDefaults = .standard) {
        defaults.removeObject(forKey: key)
    }

    @discardableResult
    static func prepare(folder: URL, backups: URL, identity: String,
                        defaults: UserDefaults = .standard) throws -> URL? {
        guard !identity.isEmpty else { throw CocoaError(.fileReadUnknown) }
        if defaults.string(forKey: key) == identity { return nil }
        let fm = FileManager.default
        var backup: URL?
        if fm.fileExists(atPath: folder.path) {
            let values = try folder.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
            guard values.isDirectory == true, values.isSymbolicLink != true else { throw CocoaError(.fileReadInvalidFileName) }
            try fm.createDirectory(at: backups, withIntermediateDirectories: true)
            let target = backups.appendingPathComponent("\(folder.lastPathComponent)-\(UUID().uuidString)", isDirectory: true)
            try fm.moveItem(at: folder, to: target)
            backup = target
        }
        try fm.createDirectory(at: folder, withIntermediateDirectories: true)
        // A crash before this write only causes another conservative backup. The engine
        // cannot run until prepare returns, and a failure never authorizes an upload.
        defaults.set(identity, forKey: key)
        return backup
    }
}
