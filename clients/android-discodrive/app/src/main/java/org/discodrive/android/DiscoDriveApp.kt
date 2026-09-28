package org.discodrive.android

import android.app.Application
import org.discodrive.android.autoupload.FolderObservers

class DiscoDriveApp : Application() {
    override fun onCreate() {
        super.onCreate()
        // Instant auto-upload watches its folders for as long as the process lives, not
        // only while its screen is open: after a relaunch new photos would otherwise wait
        // for the periodic worker, which the system may defer for hours.
        runCatching { FolderObservers.get(this).start() }
    }
}
