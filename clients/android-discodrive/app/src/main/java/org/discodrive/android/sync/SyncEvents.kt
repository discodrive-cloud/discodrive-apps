package org.discodrive.android.sync

import android.content.Context
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
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
    @Volatile private var running = false
    @Volatile private var generation = 0L
    private val commands = Mutex()

    fun start(context: Context) {
        val app = context.applicationContext
        val prefs = Prefs(app)
        if (running || !prefs.folderSync || prefs.deviceToken == null) return
        running = true
        val current = ++generation
        kick = Runnable { SyncWorker.syncNow(app) }
        CoroutineScope(Dispatchers.IO).launch {
            commands.withLock {
                if (current != generation || !running) return@withLock
                runCatching {
                    SyncHolder.use(app) { client -> client.startEvents(object : EventListener {
                        override fun onChange() {
                            main.post {
                                if (running && current == generation) {
                                    main.removeCallbacks(kick)
                                    main.postDelayed(kick, 2_000)
                                }
                            }
                        }
                    }) }
                }.onFailure { main.post { if (current == generation) running = false } }
            }
        }
    }

    fun stop(context: Context) {
        if (!running) return
        running = false
        val current = ++generation
        main.removeCallbacks(kick)
        val app = context.applicationContext
        CoroutineScope(Dispatchers.IO).launch {
            commands.withLock {
                if (current == generation) runCatching { SyncHolder.use(app) { it.stopEvents() } }
            }
        }
    }

    private var kick = Runnable {}
}
