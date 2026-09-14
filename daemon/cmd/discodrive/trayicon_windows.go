//go:build tray && windows

package main

import (
	_ "embed"

	"fyne.io/systray"
)

// Windows trays are drawn in colour and fyne/systray needs ICO bytes there — a PNG
// renders as an empty slot. No template rendering to dim, so the icon is the same in
// every state and the menu's status line tells the rest.
//
//go:embed icon.ico
var trayIconICO []byte

func applyTrayIcon(bool) { systray.SetIcon(trayIconICO) }
