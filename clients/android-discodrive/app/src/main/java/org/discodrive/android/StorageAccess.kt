package org.discodrive.android

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.Settings

/**
 * Access to the phone's shared storage, which the two builds get differently.
 *
 * full: All Files Access, granted on a system settings page. Folder sync and auto-upload
 * read any folder by path.
 *
 * play: Google Play keeps All Files Access for a few kinds of app, so this build asks for
 * photos and videos instead. With them, the paths auto-upload reads keep working, but only
 * media files show up in a listing: a play rule is media only by construction, and folder
 * sync is not offered.
 */
object StorageAccess {
    /** Runtime permissions the play build asks for (its manifest declares them). */
    val mediaPermissions: Array<String> =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) arrayOf(
            Manifest.permission.READ_MEDIA_IMAGES,
            Manifest.permission.READ_MEDIA_VIDEO,
            // Without it the photos' location is stripped from what the app reads, and the
            // copy on the server would lose it.
            Manifest.permission.ACCESS_MEDIA_LOCATION,
        ) else arrayOf(
            Manifest.permission.READ_EXTERNAL_STORAGE,
            Manifest.permission.ACCESS_MEDIA_LOCATION,
        )

    /**
     * Whether auto-upload can see the user's media. On Android 14 "Allow limited access"
     * grants a hand-picked set of photos only, which an automatic backup cannot work with:
     * that counts as not granted.
     */
    fun granted(context: Context): Boolean {
        if (BuildConfig.ALL_FILES_ACCESS) return Environment.isExternalStorageManager()
        val read = mediaPermissions.filter { it != Manifest.permission.ACCESS_MEDIA_LOCATION }
        return read.all { context.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
    }

    /** The full build's grant page. */
    fun allFilesSettings(context: Context) = Intent(
        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
        Uri.parse("package:" + context.packageName),
    )

    /** Where a permission denied for good can still be granted by hand. */
    fun appSettings(context: Context) = Intent(
        Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
        Uri.parse("package:" + context.packageName),
    )
}
