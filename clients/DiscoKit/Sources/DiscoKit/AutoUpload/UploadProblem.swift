import Foundation

/// What went wrong with an auto-upload, in terms a person can act on. The raw error text
/// stays in the queue record for diagnostics; the screen only ever shows one of these.
public enum UploadProblem: String, Codable, Sendable, CaseIterable {
    case diskFull, network, server, auth, other

    public static func classify(_ error: Error) -> UploadProblem {
        switch error {
        case APIError.notAuthenticated: return .auth
        case APIError.http(let status) where status == 401 || status == 403: return .auth
        case APIError.http(let status) where status >= 500: return .server
        case is APIError: return .other
        default: break
        }
        if isOutOfSpace(error as NSError) { return .diskFull }
        if let url = error as? URLError, networkCodes.contains(url.code) { return .network }
        return .other
    }

    /// The localization key of the sentence shown for this problem.
    public var messageKey: String { "au.err.\(rawValue)" }

    /// Failures that clear up on their own: a single one is not worth alarming anyone.
    public var isTransient: Bool { self == .network }

    private static let networkCodes: Set<URLError.Code> = [
        .timedOut, .notConnectedToInternet, .networkConnectionLost, .cannotConnectToHost,
        .cannotFindHost, .dnsLookupFailed, .internationalRoamingOff, .dataNotAllowed,
        .callIsActive, .backgroundSessionWasDisconnected,
    ]

    /// Foundation reports a full disk as NSFileWriteOutOfSpaceError, as a bare ENOSPC, or
    /// as some other write error with ENOSPC underneath.
    private static func isOutOfSpace(_ error: NSError) -> Bool {
        var current: NSError? = error
        while let e = current {
            if e.domain == NSCocoaErrorDomain && e.code == NSFileWriteOutOfSpaceError { return true }
            if e.domain == NSPOSIXErrorDomain && e.code == Int(ENOSPC) { return true }
            current = e.userInfo[NSUnderlyingErrorKey] as? NSError
        }
        return false
    }
}

/// How many exported copies may wait on the device for the upload queue.
///
/// Exports are full-size originals (a ProRAW photo is ~100 MB), so a count limit alone
/// let a near-full phone run out of space halfway through a batch.
public enum StagingBudget {
    public enum Decision: Equatable, Sendable { case export, queueFull, lowSpace }

    /// Copies waiting for upload may take up to this much.
    public static let maxStagedBytes: Int64 = 1 << 30
    /// Never take the device below this: the system and other apps need room too.
    public static let reserveBytes: Int64 = 300 << 20

    /// `freeBytes` is nil when the volume cannot say; the queue cap still applies.
    public static func decide(stagedBytes: Int64, queuedCount: Int, freeBytes: Int64?) -> Decision {
        if let freeBytes, freeBytes < reserveBytes { return .lowSpace }
        if queuedCount > 0 && stagedBytes >= maxStagedBytes { return .queueFull }
        return .export
    }
}

extension PhotoTransfer {
    /// The problem worth showing for a queue, or nil when none is current. A failure older
    /// than the last progress is no longer true; a single network hiccup retries quietly.
    public static func visibleProblem(in jobs: [PhotoTransfer], lastProgress: Date?) -> UploadProblem? {
        jobs.filter { job in
            guard job.lastError != nil else { return false }
            let problem = job.problem ?? .other
            if problem.isTransient && job.attempts < 3 { return false }
            guard let lastProgress, let failedAt = job.failedAt else { return true }
            return failedAt > lastProgress
        }
        .max { ($0.failedAt ?? .distantPast) < ($1.failedAt ?? .distantPast) }
        .map { $0.problem ?? .other }
    }
}
