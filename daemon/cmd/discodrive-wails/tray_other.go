//go:build !windows && !darwin

package main

import (
	_ "embed"

	"fyne.io/systray"
)

// Linux trays are drawn in colour; fyne/systray accepts a PNG.
//
//go:embed appicon.png
var trayIcon []byte

func setTrayIcon() { systray.SetIcon(trayIcon) }
