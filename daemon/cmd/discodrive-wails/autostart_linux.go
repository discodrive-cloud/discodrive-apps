package main

import (
	"os"
	"path/filepath"
)

// applyOpenAtLogin writes/removes an XDG autostart entry so the desktop session launches
// the app at login (~/.config/autostart/discodrive.desktop). When minimized, the entry
// passes the --hidden flag so the app starts in the tray.
func applyOpenAtLogin(enabled, minimized bool) error {
	cfg, err := os.UserConfigDir() // ~/.config (honours XDG_CONFIG_HOME)
	if err != nil {
		return err
	}
	dir := filepath.Join(cfg, "autostart")
	entry := filepath.Join(dir, "discodrive.desktop")

	if !enabled {
		if err := os.Remove(entry); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	flag := ""
	if minimized {
		flag = hiddenFlag
	}
	return os.WriteFile(entry, []byte(linuxDesktopEntry(exe, flag, linuxIconPath())), 0o644)
}
