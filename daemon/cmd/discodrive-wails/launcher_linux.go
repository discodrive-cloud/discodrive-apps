//go:build linux

package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

// linuxOptions names the window after the launcher entry and gives it the app icon.
// WebviewGpuPolicyNever is what Wails applies when no Linux options are passed, so
// hardware acceleration stays as it was.
func linuxOptions() *linux.Options {
	return &linux.Options{
		Icon:             trayIcon,
		ProgramName:      linuxAppID,
		WebviewGpuPolicy: linux.WebviewGpuPolicyNever,
	}
}

// dataHome is $XDG_DATA_HOME, or ~/.local/share when it is unset.
func dataHome() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

// linuxIconPath is where the app icon is kept for launcher and autostart entries.
func linuxIconPath() string {
	d, err := dataHome()
	if err != nil {
		return ""
	}
	return filepath.Join(d, linuxAppID, linuxAppID+".png")
}

// installLauncher writes the icon and an applications entry for this executable, so
// the dock shows the DiscoDrive icon for its window. Files are rewritten only when
// they differ, so a moved binary updates its entry on the next start.
func installLauncher() {
	d, err := dataHome()
	if err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	icon := linuxIconPath()
	if err := writeIfChanged(icon, trayIcon); err != nil {
		log.Printf("launcher icon: %v", err)
		icon = ""
	}
	entry := filepath.Join(d, "applications", linuxAppID+".desktop")
	if err := writeIfChanged(entry, []byte(linuxLauncherEntry(exe, icon))); err != nil {
		log.Printf("launcher entry: %v", err)
	}
}

func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
