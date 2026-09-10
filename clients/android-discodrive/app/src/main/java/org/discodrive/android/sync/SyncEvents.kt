package org.discodrive.android.sync

import android.content.Context
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import mobile.EventListener
import org.discodrive.android.Prefs

/**
 * Keeps the server's event stream open while the app is on screen, so a change made
 * elsewhere reaches the folder within seconds instead of at the next periodic pass.
 *
 * Only while on screen: a stream needs a live process, and a foreground service that
 * holds one is what Android 14 rations and Android 15 stops. Off screen, the periodic
 * worker is all there is. Events within two seconds of each other become one pass.
 */
object SyncEvents {
    private val main = Handler(Looper.getMainLooper())
    private var running = false

    fun start(context: Context) {
        val app = context.applicationContext
        val prefs = Prefs(app)
        if (running || !prefs.folderSync || prefs.deviceToken == null) return
        running = true
        kick = Runnable { SyncWorker.syncNow(app) }
        CoroutineScope(Dispatchers.IO).launch {
            SyncHolder.get(app)?.startEvents(object : EventListener {
                override fun onChange() {
                    main.removeCallbacks(kick)
                    main.postDelayed(kick, 2_000)
                }
            })
        }
    }

    fun stop(context: Context) {
        if (!running) return
        running = false
        main.removeCallbacks(kick)
        val app = context.applicationContext
        CoroutineScope(Dispatchers.IO).launch { SyncHolder.get(app)?.stopEvents() }
    }

    private var kick = Runnable {}
}
