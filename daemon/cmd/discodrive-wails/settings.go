package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
)

// Settings is the desktop client's local preferences, stored as JSON in the profile.
type Settings struct {
	Theme       string `json:"theme"` // "dark" | "light"
	Lang        string `json:"lang"`  // one of the 7 supported locales (en/ru/uk/de/fr/es/sr)
	OpenAtLogin bool   `json:"openAtLogin"`
	// StartMinimized launches the app hidden to the tray; only takes effect via the
	// open-at-login registration (the autostart command gets a --hidden flag).
	StartMinimized bool `json:"startMinimized"`
}

func settingsPath(profile string) string { return filepath.Join(profile, "settings.json") }

// loadSettings reads settings.json, returning sensible defaults when absent/invalid.
func loadSettings(profile string) Settings {
	s := Settings{Theme: "dark", Lang: "en"}
	data, err := os.ReadFile(settingsPath(profile))
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s)
	if s.Theme == "" {
		s.Theme = "dark"
	}
	if s.Lang == "" {
		s.Lang = "en"
	}
	return s
}

func saveSettings(profile string, s Settings) error {
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(profile), data, 0o600)
}

// GetSettings returns the stored desktop preferences.
func (a *App) GetSettings() Settings {
	profile, err := desktop.ProfileDir()
	if err != nil {
		return Settings{Theme: "dark", Lang: "en"}
	}
	return loadSettings(profile)
}

// SaveSettings persists the preferences and applies the open-at-login registration.
func (a *App) SaveSettings(s Settings) error {
	profile, err := desktop.ProfileDir()
	if err != nil {
		return err
	}
	if err := saveSettings(profile, s); err != nil {
		return err
	}
	return applyOpenAtLogin(s.OpenAtLogin, s.StartMinimized)
}

// applyOpenAtLogin registers/unregisters the app to launch at login, passing the
// --hidden flag when minimized. The implementation is platform-specific — see
// autostart_{darwin,windows,linux}.go (macOS LaunchAgent, Windows HKCU Run key,
// Linux XDG autostart).

// ServerURL returns the paired server's URL (empty if not paired).
func (a *App) ServerURL() string {
	profile, err := desktop.ProfileDir()
	if err != nil {
		return ""
	}
	cfg, err := config.Load(desktop.DesktopConfigPath(profile))
	if err != nil {
		return ""
	}
	return cfg.ServerURL
}

// CachePath returns the local on-demand content/cache directory.
func (a *App) CachePath() string {
	profile, err := desktop.ProfileDir()
	if err != nil {
		return ""
	}
	return desktop.ContentDir(profile)
}

// RevealCache opens the cache directory in the OS file manager.
func (a *App) RevealCache() {
	p := a.CachePath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(p, 0o700)
	openLocal(p)
}

// Unpair closes any open vaults, removes the saved config, and wipes the profile's
// server-derived state (index + content cache) so the UI returns to the pairing
// screen with nothing left of the old server. Without the wipe, pairing to a
// different server merged the stale index into the new tree and re-uploaded
// leftover data (e.g. vaults) to the wrong server.
func (a *App) Unpair() error {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	if a.mirror != nil {
		if err := a.mirror.Detach(); err != nil {
			return err
		}
	}
	if a.ready {
		// While the config still points at the old server: re-encrypt and push any
		// open vaults back where they belong.
		_ = a.ctrl.CloseAllVaults(a.ctx)
	}
	profile, err := desktop.ProfileDir()
	if err != nil {
		return err
	}
	if a.idx != nil {
		_ = a.idx.Close() // release index.db so it can be deleted (Windows)
	}
	a.ctrl, a.idx, a.up, a.ready = nil, nil, nil, false
	if err := os.Remove(desktop.DesktopConfigPath(profile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return desktop.WipeState(profile)
}
