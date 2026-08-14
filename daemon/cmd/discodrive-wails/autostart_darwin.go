package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const launchAgentLabel = "com.wails.discodrive-wails"

// applyOpenAtLogin registers/unregisters a macOS LaunchAgent that runs the app at login.
// When minimized, the LaunchAgent passes the --hidden flag so the app starts in the tray.
//
// Both paths unregister the agent before touching the file. Deleting the plist on its own
// leaves launchd holding the registration for the rest of the session — pointing at a binary
// that may since have been moved or deleted — and a later re-enable can then fail because the
// label is already loaded. The agent is deliberately not bootstrapped back in when enabling:
// it carries RunAtLoad, so launchd would start a second copy of the app on the spot, and the
// point of the setting is the next login, not this one.
func applyOpenAtLogin(enabled, minimized bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.wails.discodrive-wails.plist")
	// Fails when nothing is registered, which is the common case — hence the ignored error.
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)).Run()
	if !enabled {
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := fmt.Sprintf("<string>%s</string>", exe)
	if minimized {
		args += fmt.Sprintf("<string>%s</string>", hiddenFlag)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array>%s</array>
  <key>RunAtLoad</key><true/>
</dict></plist>
`, launchAgentLabel, args)
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(plistPath, []byte(plist), 0o644)
}
