import Foundation
#if os(macOS)
import CoreServices
#endif

/// Marks a file the app wrote from server bytes as downloaded, so macOS runs Gatekeeper on
/// it when it is opened. The name is the server's to choose — `report.pdf.terminal`,
/// `invoice.webloc` — and without the mark such a file opens like one the user made.
/// Failures are ignored: the mark is a safeguard, not a reason to lose the download.
/// iOS has no quarantine; there it does nothing.
public enum DownloadQuarantine {
    public static let agentName = "DiscoDrive"

    public static func mark(_ url: URL) {
        #if os(macOS)
        var values = URLResourceValues()
        values.quarantineProperties = [
            kLSQuarantineTypeKey as String: kLSQuarantineTypeOtherDownload as String,
            kLSQuarantineAgentNameKey as String: agentName,
        ]
        var target = url
        try? target.setResourceValues(values)
        #endif
    }
}

/// Downloads set aside at logout (`content.old-<date>`): kept for a while in case they held
/// something the user still wants, then removed. Nothing else ever cleaned them up.
public enum SetAsideDownloads {
    public static let prefix = "content.old-"
    /// How long a set-aside folder is kept.
    public static let maxAge: TimeInterval = 7 * 86_400

    /// The folder name for downloads set aside at `date`. ISO 8601 with the colons replaced:
    /// a colon is a path separator to Finder.
    public static func name(for date: Date) -> String {
        prefix + formatter().string(from: date).replacingOccurrences(of: ":", with: "-")
    }

    /// The date a set-aside folder's name carries, nil when it carries none.
    public static func date(fromName name: String) -> Date? {
        guard name.hasPrefix(prefix) else { return nil }
        var stamp = String(name.dropFirst(prefix.count))
        // yyyy-MM-ddTHH-mm-ssZ: put the time's colons back.
        guard let t = stamp.firstIndex(of: "T") else { return nil }
        let time = stamp[stamp.index(after: t)...].replacingOccurrences(of: "-", with: ":")
        stamp = String(stamp[...t]) + time
        return formatter().date(from: stamp)
    }

    /// Removes the set-aside folders in `directory` older than `maxAge` (0: all of them);
    /// returns what was removed. The age is the date in the name, or the folder's own
    /// modification date when the name has none.
    @discardableResult
    public static func prune(in directory: URL, olderThan maxAge: TimeInterval, now: Date = Date()) -> [URL] {
        let fm = FileManager.default
        guard let items = try? fm.contentsOfDirectory(at: directory, includingPropertiesForKeys: [.contentModificationDateKey],
                                                      options: []) else { return [] }
        var removed: [URL] = []
        for url in items.sorted(by: { $0.lastPathComponent < $1.lastPathComponent })
        where url.lastPathComponent.hasPrefix(prefix) {
            let when = date(fromName: url.lastPathComponent)
                ?? (try? url.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
                ?? .distantPast
            guard now.timeIntervalSince(when) >= maxAge else { continue }
            if (try? fm.removeItem(at: url)) != nil { removed.append(url) }
        }
        return removed
    }

    private static func formatter() -> ISO8601DateFormatter { ISO8601DateFormatter() }
}
