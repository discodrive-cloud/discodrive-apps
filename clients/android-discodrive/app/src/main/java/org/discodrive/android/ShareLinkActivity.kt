package org.discodrive.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import mobile.Mobile

/**
 * "Share → DiscoDrive": takes the link, asks the server once (≤ 1.5 s), shows one toast and
 * closes. No screen of its own. A link that could not get through in time waits in
 * [LinkOutbox]; the app and the next share send it.
 */
class ShareLinkActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val text = if (intent?.action == Intent.ACTION_SEND) intent.getStringExtra(Intent.EXTRA_TEXT) else null
        val subject = intent?.getStringExtra(Intent.EXTRA_SUBJECT)
        val app = applicationContext
        scope.launch {
            val outcome = save(app, text, subject)
            withContext(Dispatchers.Main) {
                Toast.makeText(app, message(app, outcome), if (outcome == LinkShare.Outcome.SAVED || outcome == LinkShare.Outcome.QUEUED) Toast.LENGTH_SHORT else Toast.LENGTH_LONG).show()
            }
        }
        finish()
    }

    companion object {
        // Outlives the activity, which closes at once: the request may still be in flight.
        private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        // One app-start flush at a time: onResume fires often.
        private val flushing = java.util.concurrent.atomic.AtomicBoolean(false)

        fun message(context: Context, outcome: LinkShare.Outcome): String = context.getString(
            when (outcome) {
                LinkShare.Outcome.SAVED, LinkShare.Outcome.QUEUED -> R.string.share_saved
                LinkShare.Outcome.SIGN_IN -> R.string.share_sign_in
                LinkShare.Outcome.REJECTED -> R.string.share_rejected
            }
        )

        private fun send(prefs: Prefs, url: String, title: String?, sharedAtMs: Long = 0L): Int? {
            val token = prefs.deviceToken ?: return 401
            return Mobile.saveLink(prefs.serverURL, token, prefs.serverPin, url, title ?: "", sharedAtMs).toInt()
        }

        suspend fun save(context: Context, text: String?, subject: String?): LinkShare.Outcome {
            val url = text?.let(LinkShare::firstWebUrl) ?: return LinkShare.Outcome.REJECTED
            val prefs = Prefs(context)
            if (prefs.deviceToken == null || !UrlPolicy.serverUrlAllowed(prefs.serverURL)) return LinkShare.Outcome.SIGN_IN
            val title = LinkShare.title(text, url) ?: subject?.trim()?.takeIf { it.isNotEmpty() }
            val outbox = prefs.linkOutbox()
            val call = scope.async { runCatching { send(prefs, url, title) }.getOrNull() }
            val status = withTimeoutOrNull(LinkShare.ANSWER_TIMEOUT_MS) { call.await() }
            val outcome = LinkShare.outcome(status)
            when (outcome) {
                LinkShare.Outcome.QUEUED -> {
                    val id = outbox.enqueue(url, title)
                    // The blocking call cannot be cancelled: if it gets through after all,
                    // its queued copy is not sent a second time.
                    scope.launch {
                        val late = runCatching { call.await() }.getOrNull()
                        if (LinkShare.outcome(late) != LinkShare.Outcome.QUEUED) outbox.remove(id)
                    }
                }
                // Earlier shares go out in the background: the toast does not wait for them.
                LinkShare.Outcome.SAVED -> scope.launch { LinkShare.flush(outbox) { u, t, at -> send(prefs, u, t, at) } }
                else -> {}
            }
            return outcome
        }

        /** Sends links earlier shares left behind; call when the app comes to the foreground. */
        fun flushQueued(context: Context) {
            scope.launch {
                val prefs = Prefs(context)
                if (prefs.deviceToken == null) return@launch
                if (!flushing.compareAndSet(false, true)) return@launch
                try { LinkShare.flush(prefs.linkOutbox()) { u, t, at -> send(prefs, u, t, at) } } finally { flushing.set(false) }
            }
        }
    }
}
