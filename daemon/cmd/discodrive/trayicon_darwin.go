//go:build tray && darwin

package main

import (
	_ "embed"

	"fyne.io/systray"
)

// The menu bar takes template images — the logo's silhouette, which the system tints
// for light and dark bars — and a dimmed copy for the "out of reach" state.
//
//go:embed tray-logo.png
var trayTemplate []byte

//go:embed tray-logo-dim.png
var trayTemplateDim []byte

func applyTrayIcon(dim bool) {
	if dim {
		systray.SetTemplateIcon(trayTemplateDim, trayIcon)
	} else {
		systray.SetTemplateIcon(trayTemplate, trayIcon)
	}
}
