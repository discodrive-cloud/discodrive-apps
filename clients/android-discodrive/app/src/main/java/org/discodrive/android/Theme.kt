package org.discodrive.android

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
 * Two things went wrong without one. A bare MaterialTheme always uses the light colour scheme,
 * while the window background comes from the DayNight theme in the manifest — so with the phone
 * in dark mode, screens that paint no background of their own (pairing, the permission gate)
 * put near-black text on the system's dark background, which was barely readable. Screens built
 * on Scaffold, meanwhile, painted a white surface whatever the phone was set to, so past the
 * pairing screen the app looked like it had never heard of dark mode.
 *
 * Following the system setting fixes the palette; the Surface makes every screen paint that
 * palette's background instead of inheriting the window's.
 */
@Composable
fun DiscoDriveTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(colorScheme = if (darkTheme) darkColorScheme() else lightColorScheme()) {
        Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
            content()
        }
    }
}
