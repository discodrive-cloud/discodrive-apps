import Foundation

enum FullSyncStorage {
    static func prepare(folder: URL, state: URL, backups: URL) throws -> URL? {
        let fm = FileManager.default
        let marker = state.appendingPathComponent("prepared")
        let previous = state.appendingPathComponent("backup-path")
        if fm.fileExists(atPath: marker.path), fm.fileExists(atPath: state.appendingPathComponent("state.db").path) {
            return (try? String(contentsOf: previous, encoding: .utf8)).map { URL(fileURLWithPath: $0) }
        }
        let entries = try fm.contentsOfDirectory(at: folder, includingPropertiesForKeys: nil)
            .filter { $0.lastPathComponent != ".DS_Store" }
        var destination: URL?
        if !entries.isEmpty {
            let target = backups.appendingPathComponent("\(folder.lastPathComponent)-\(UUID().uuidString)")
            try fm.createDirectory(at: target, withIntermediateDirectories: true)
            // Save the location before moving anything so an interrupted preparation can
            // still be inspected. FileManager also handles moves between volumes.
            try target.path.write(to: previous, atomically: true, encoding: .utf8)
            for entry in entries { try fm.moveItem(at: entry, to: target.appendingPathComponent(entry.lastPathComponent)) }
            destination = target
        }
        try Data().write(to: marker, options: .atomic)
        return destination
    }

}
