//go:build tray && !darwin && !windows

package main

import "fyne.io/systray"

// Linux trays are drawn in colour; there is no template rendering to dim,
// so the icon stays the same in every state and the menu's status line tells the rest.
func applyTrayIcon(bool) { systray.SetIcon(trayIcon) }
