package org.discodrive.android

/** Longest name most filesystems (and the server) accept for one path segment. */
const val MAX_NAME_LENGTH = 255

/**
 * Whether a display name reported by another app's content provider can be used as a file
 * name here. That name is whatever the other app says: a separator or `..` in it would point
 * the write somewhere other than the folder on screen.
 */
fun isValidUploadName(name: String): Boolean =
    name.isNotBlank() &&
        name != "." && name != ".." &&
        !name.contains('/') && !name.contains('\\') &&
        name.length <= MAX_NAME_LENGTH
