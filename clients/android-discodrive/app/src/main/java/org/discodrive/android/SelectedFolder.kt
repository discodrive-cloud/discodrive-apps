package org.discodrive.android

/** Capture the selection before waiting for the browser, and discard stale results. */
internal suspend fun resolveSelectedFolder(
    currentFolder: () -> String,
    resolve: suspend (String) -> String?,
): String? {
    val selected = currentFolder()
    val path = resolve(selected)
    return path.takeIf { currentFolder() == selected }
}
