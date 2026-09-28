import Foundation

/// What sits at a name in the destination folder.
public enum NameState: Sendable, Equatable {
    case absent
    /// A file with the same content is already there.
    case same
    /// The name is taken by other content, or by a folder.
    case different
}

/// What ``NameResolver/resolve(_:exists:)`` decided.
public enum NameResolution: Sendable, Equatable {
    /// Upload under this name.
    case upload(String)
    /// The identical bytes are already there: nothing to send.
    case alreadyThere
    /// Every candidate is taken by other content: try again later — it was NOT sent.
    case noFreeName
}

/// Decides what name a file should land under.
///
/// The server treats an upload with an existing name as a new version of that file, so a
/// photo named like one already there would quietly replace it. Every upload asks first,
/// and a taken name gets a `-1`, `-2`, … suffix instead.
public enum NameResolver {
    /// Beyond this something is wrong with the destination; deferring beats looping.
    static let maxTries = 50

    /// Returns the name to upload under, `.alreadyThere` when the identical bytes are
    /// already there, or `.noFreeName`.
    public static func resolve(_ name: String, exists: (String) -> NameState) -> NameResolution {
        for attempt in 0...maxTries {
            let candidate = attempt == 0 ? name : suffixed(name, attempt)
            switch exists(candidate) {
            case .absent: return .upload(candidate)
            case .same: return .alreadyThere
            case .different: continue
            }
        }
        return .noFreeName
    }

    /// The name a photo goes up under. An edited photo's full-size resource is always
    /// called `FullSizeRender.*`, so the original resource's name is kept, with the
    /// extension of the resource actually sent (an edit of a HEIC is a JPEG).
    public static func uploadName(resource: String, original: String?) -> String {
        guard let original, original != resource else { return resource }
        let base = (original as NSString).deletingPathExtension
        let ext = (resource as NSString).pathExtension
        return ext.isEmpty ? base : base + "." + ext
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
