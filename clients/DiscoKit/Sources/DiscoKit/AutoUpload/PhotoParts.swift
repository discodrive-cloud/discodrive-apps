import Foundation

/// One file of a photo-library asset. The server's gallery puts them back together: the
/// edit by name (`<stem>_E.*` beside `<stem>.*`), the Live video by its content identifier.
public enum PhotoPart: String, Codable, Sendable, CaseIterable {
    /// The camera's file. Never changes once taken.
    case original
    /// The adjusted render; follows every edit and goes when the edit is reverted.
    case edit
    /// A Live Photo's video.
    case live
}

/// What the journal knows about a part on the server.
public struct RecordedPart: Sendable, Equatable {
    public let sha: String
    public let serverName: String
    public init(sha: String, serverName: String) { self.sha = sha; self.serverName = serverName }
}

/// An upload made before assets went up in parts: one file, the edit when there was one,
/// under the original's base name.
public struct LegacyUpload: Sendable, Equatable {
    public let modified: Date
    public let sha: String?
    public let serverName: String?
    public init(modified: Date, sha: String?, serverName: String?) {
        self.modified = modified; self.sha = sha; self.serverName = serverName
    }
}

public enum PhotoParts {
    /// `IMG_1234.HEIC` + `FullSizeRender.jpg` → `IMG_1234_E.jpg`.
    public static func editName(original: String, resource: String) -> String {
        name(base: stem(original) + "_E", ext: ext(resource))
    }

    /// `IMG_1234-1.HEIC` + `IMG_1234.MOV` → `IMG_1234-1.MOV`.
    public static func liveName(original: String, resource: String) -> String {
        name(base: stem(original), ext: ext(resource))
    }

    /// The original's name beside a file named `base`: `IMG_1234.JPG` + `IMG_1234.HEIC` → `IMG_1234.HEIC`.
    public static func originalName(base: String, resource: String) -> String {
        name(base: stem(base), ext: ext(resource))
    }

    /// The original's name beside its edit: `IMG_1234-1_E.JPG` + `IMG_1234.HEIC` → `IMG_1234-1.HEIC`.
    /// Nil when `edit` is not an edit name.
    public static func originalName(forEdit edit: String, resource: String) -> String? {
        let base = stem(edit)
        guard base.count > 2, base.hasSuffix("_E") else { return nil }
        return name(base: String(base.dropLast(2)), ext: ext(resource))
    }

    public enum LegacyKind: Sendable, Equatable { case original, edit }

    /// What a former version sent for an asset. It sent the full-size render whenever the
    /// asset had one: untouched since the upload, an asset with an edit sent its edit and
    /// one without sent its original. Changed since (edited, or the edit reverted), only
    /// the original's bytes tell.
    public static func legacyKind(hasEdit: Bool, modifiedUnchanged: Bool,
                                  originalSHA: String?, legacySHA: String?) -> LegacyKind {
        if modifiedUnchanged { return hasEdit ? .edit : .original }
        if let originalSHA, originalSHA == legacySHA { return .original }
        return .edit
    }

    private static func stem(_ name: String) -> String { (name as NSString).deletingPathExtension }
    private static func ext(_ name: String) -> String { (name as NSString).pathExtension }
    private static func name(base: String, ext: String) -> String { ext.isEmpty ? base : base + "." + ext }
}
