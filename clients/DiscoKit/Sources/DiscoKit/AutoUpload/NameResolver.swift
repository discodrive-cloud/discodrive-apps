import Foundation

/// What sits at a name in the destination folder.
public enum NameState: Sendable, Equatable {
    case absent
    /// A file with the same content is already there.
    case same
    /// The name is taken by other content, or by a folder.
    case different
}

/// Decides what name a file should land under.
///
/// The server treats an upload with an existing name as a new version of that file, so a
/// photo named like one already there would quietly replace it. Every upload asks first,
/// and a taken name gets a `-1`, `-2`, … suffix instead.
public enum NameResolver {
    /// Beyond this something is wrong with the destination; deferring beats looping.
    static let maxTries = 50

    /// Returns the name to upload under, or nil when the file should be skipped — either
    /// the identical bytes are already there, or no free name was found.
    public static func resolve(_ name: String, exists: (String) -> NameState) -> String? {
        for attempt in 0...maxTries {
            let candidate = attempt == 0 ? name : suffixed(name, attempt)
            switch exists(candidate) {
            case .absent: return candidate
            case .same: return nil
            case .different: continue
            }
        }
        return nil
    }

    /// `IMG_1.jpg` + 2 → `IMG_1-2.jpg`; keeps dotfiles and multi-part extensions sane.
    static func suffixed(_ name: String, _ n: Int) -> String {
        guard let dot = name.lastIndex(of: "."), dot != name.startIndex else {
            return "\(name)-\(n)"
        }
        let base = name[name.startIndex..<dot]
        let ext = name[dot...]
        return "\(base)-\(n)\(ext)"
    }
}
