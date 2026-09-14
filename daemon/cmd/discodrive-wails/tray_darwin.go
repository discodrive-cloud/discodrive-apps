//go:build darwin

package main

import (
	_ "embed"

	"fyne.io/systray"
)

// The menu bar takes a template image — the logo's silhouette, tinted by the system for
// light and dark bars, the same glyph the native app and Finder show. The coloured icon
// is the fallback systray wants alongside it.
//
//go:embed tray-logo.png
var trayTemplate []byte

//go:embed appicon.png
var trayIcon []byte

func setTrayIcon() { systray.SetTemplateIcon(trayTemplate, trayIcon) }
