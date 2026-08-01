package org.discodrive.android.autoupload

import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.os.BatteryManager
import org.discodrive.android.Prefs

/** Why a pass is not running right now — shown in the UI so "nothing happens" is explainable. */
enum class Block { NONE, NO_NETWORK, NEEDS_WIFI, ROAMING, NOT_CHARGING, LOW_BATTERY }

/**
 * The network and power rules that gate an upload pass. Global rather than per-rule: nobody
 * wants their camera folder on Wi-Fi only and their downloads folder on mobile data.
 */
object Conditions {
    /** Below this an upload competes with the user's remaining battery. */
    const val MIN_BATTERY_PERCENT = 20

    fun check(context: Context, prefs: Prefs): Block {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        val caps = cm?.activeNetwork?.let { cm.getNetworkCapabilities(it) }
            ?: return Block.NO_NETWORK
        if (!caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) return Block.NO_NETWORK

        val unmetered = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
        if (prefs.wifiOnly && !unmetered) return Block.NEEDS_WIFI
        if (prefs.pauseOnRoaming && !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_ROAMING)) {
            return Block.ROAMING
        }

        val status = context.registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
        val plugged = (status?.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0) ?: 0) != 0
        if (prefs.whileChargingOnly && !plugged) return Block.NOT_CHARGING
        if (prefs.requireBattery && !plugged) {
            val level = status?.getIntExtra(BatteryManager.EXTRA_LEVEL, -1) ?: -1
            val scale = status?.getIntExtra(BatteryManager.EXTRA_SCALE, -1) ?: -1
            if (level >= 0 && scale > 0 && level * 100 / scale < MIN_BATTERY_PERCENT) return Block.LOW_BATTERY
        }
        return Block.NONE
    }

    fun allow(context: Context, prefs: Prefs): Boolean = check(context, prefs) == Block.NONE
}
