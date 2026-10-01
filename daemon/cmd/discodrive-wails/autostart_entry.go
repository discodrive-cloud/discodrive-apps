package main

import "fmt"

// hiddenFlag is the CLI flag the autostart entry passes so an auto-launched instance
// starts minimized to the tray; manual launches omit it and show the window. Parsed in
// main(); appended to the per-platform autostart command when StartMinimized is on.
const hiddenFlag = "--hidden"

// linuxAppID names the app to the Linux desktop: it is the GTK program name (so the
// window's WM_CLASS on X11 and app_id on Wayland), the launcher's file name and its
// StartupWMClass. Docks match a window to a launcher by it; without a match GNOME shows
// a generic gear instead of the app icon.
const linuxAppID = "discodrive"

// linuxDesktopEntry builds an XDG autostart .desktop file that launches exe at login.
// Kept in its own (untagged) file so it can be unit-tested on any host; it is only used
// by the linux build. The Exec path is double-quoted per the Desktop Entry spec so paths
// with spaces are handled; the flag (if any) is appended outside the quotes.
func linuxDesktopEntry(exe, flag, icon string) string {
	exec := fmt.Sprintf(`"%s"`, exe)
	if flag != "" {
		exec += " " + flag
	}
	return linuxEntry(exec, icon) + "X-GNOME-Autostart-enabled=true\n"
}

// linuxLauncherEntry builds the applications-menu entry the dock uses to find the
// app's name and icon for its window.
func linuxLauncherEntry(exe, icon string) string {
	return linuxEntry(fmt.Sprintf(`"%s"`, exe), icon) +
		"Terminal=false\nCategories=Network;FileTransfer;\n"
}

func linuxEntry(exec, icon string) string {
	s := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=DiscoDrive\nExec=%s\nStartupWMClass=%s\n",
		exec, linuxAppID)
	if icon != "" {
		s += "Icon=" + icon + "\n"
	}
	return s
}
