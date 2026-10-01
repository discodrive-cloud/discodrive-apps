package org.discodrive.android

/**
 * The server kept part of the trash on a purge or empty-trash (409): a trashed folder still
 * holds an item that is not in the trash, so it and what it holds stay; everything else was
 * removed. Pure helpers: no Android or gomobile types.
 */
object TrashBlocked {
    /** Text of mobile.TrashBlockedMarker, copied so the gomobile class is not loaded. */
    const val MARKER = "trash kept: a folder still holds items that are not in the trash"

    fun matches(message: String?): Boolean = message?.contains(MARKER) == true

    /** The localized [explanation] in place of the core's message for a refused purge. */
    fun describe(message: String?, explanation: String): String? =
        if (matches(message)) explanation else message
}
