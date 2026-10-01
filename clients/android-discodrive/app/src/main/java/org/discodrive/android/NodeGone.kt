package org.discodrive.android

/**
 * An operation on an item the server no longer has (deleted there while this device missed
 * the delete). By the time the error arrives the core has already dropped the item and its
 * subtree from the local index, so the screen only relists and says so. Pure helpers: no
 * Android or gomobile types.
 */
object NodeGone {
    /**
     * Text of mobile.NodeGoneMarker, copied because touching the gomobile class loads the
     * native library. gomobile flattens Go errors to their message.
     */
    const val MARKER = "node no longer exists on the server"

    fun matches(message: String?): Boolean = message?.contains(MARKER) == true

    /** The localized [explanation] in place of the core's message for a gone item. */
    fun describe(message: String?, explanation: String): String? =
        if (matches(message)) explanation else message
}
