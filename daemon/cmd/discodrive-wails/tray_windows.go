//go:build windows

package main

import (
	_ "embed"

	"fyne.io/systray"
)

// On Windows fyne/systray requires ICO bytes — a PNG renders as an empty tray slot.
//
//go:embed icon.ico
var trayIcon []byte

func setTrayIcon() { systray.SetIcon(trayIcon) }
