package org.discodrive.fastsync

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier

/**
 * The app's theme.
 *
 * A bare MaterialTheme always uses the light colour scheme, while the window background comes
 * from the DayNight theme in the manifest. With the phone in dark mode that put near-black text
 * on the system's dark background on screens that paint no background of their own, and screens
 * built on Scaffold stayed white whatever the phone was set to.
 *
 * Following the system setting fixes the palette; the Surface makes every screen paint that
 * palette's background instead of inheriting the window's.
 */
@Composable
fun FastSyncTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(colorScheme = if (darkTheme) darkColorScheme() else lightColorScheme()) {
        Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
            content()
        }
    }
}
