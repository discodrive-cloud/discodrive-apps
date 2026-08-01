package org.discodrive.android.autoupload

// Answers from the Go binding's Browser.existsWithHash.
const val EXISTS_ABSENT = "absent"
const val EXISTS_SAME = "same"
const val EXISTS_DIFFERENT = "different"

/**
 * Decides what name a file should land under in the destination folder.
 *
 * The server treats an upload with an existing name as a new version of that file, so a
 * photo named like one already there would quietly replace it. Every upload therefore asks
 * first, and a taken name gets a `-1`, `-2`, … suffix instead.
 */
object NameResolver {
    /** Beyond this something is wrong with the destination; deferring beats looping. */
    private const val MAX_TRIES = 50

    /**
     * Returns the name to upload under, or null when the file should be skipped — either
     * the identical bytes are already there, or no free name was found.
     *
     * [exists] takes a candidate name and answers [EXISTS_ABSENT] / [EXISTS_SAME] /
     * [EXISTS_DIFFERENT]; it is a lambda so this stays testable without the Go layer.
     */
    fun resolve(name: String, exists: (String) -> String): String? {
        for (attempt in 0..MAX_TRIES) {
            val candidate = if (attempt == 0) name else suffixed(name, attempt)
            when (exists(candidate)) {
                EXISTS_ABSENT -> return candidate
                EXISTS_SAME -> return null
                else -> Unit // taken by other content — try the next suffix
            }
        }
        return null
    }

    /** `IMG_1.jpg` + 2 → `IMG_1-2.jpg`; keeps dotfiles and multi-part extensions sane. */
    private fun suffixed(name: String, n: Int): String {
        val dot = name.lastIndexOf('.')
        // dot == 0 is a dotfile (".config"), not an extension.
        return if (dot <= 0) "$name-$n" else "${name.substring(0, dot)}-$n${name.substring(dot)}"
    }
}
