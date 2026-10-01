package main

import (
	"context"
	"discodrive.org/daemon/internal/index"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
	"discodrive.org/daemon/internal/protocol"
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

// LegacyVaultPath reports the old shared plaintext cache without claiming its
// contents for the current account. Recovery must be an explicit user choice.
func (a *App) LegacyVaultPath() (string, error) { return desktop.LegacyVaultFolder() }

func (a *App) RevealLegacyVaults() error {
	p, err := desktop.LegacyVaultFolder()
	if err != nil {
		return err
	}
	if p != "" {
		openLocal(p)
	}
	return nil
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
//
// Open vaults are saved first, before anything else changes, and decrypted files an
// earlier session left behind are looked for. If a vault cannot be saved, or such files
// are found (they may hold unsaved changes), unpairing stops there with a
// "vault_save_failed:" error and leaves everything as it was: the session with its
// unsaved changes, the decrypted files, the pairing and the index. The user can then
// retry, or sign out anyway (UnpairAnyway), which keeps that work in a recovery folder.
func (a *App) Unpair() error {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	ctrl, idx, _, ok := a.account()
	dir, err := recoveryDir()
	if err != nil {
		return err
	}
	if ok {
		// While the config still points at the old server: re-encrypt and push any
		// open vaults back where they belong.
		if err := closeOpenVaults(a.ctx, ctrl); err != nil {
			return fmt.Errorf("%s %w", errVaultSaveFailed, err)
		}
		if _, err := sweepVaultLeftovers(ctrl, dir, false); err != nil {
			return fmt.Errorf("%s %w", errVaultSaveFailed, err)
		}
	} else if _, err := a.sweepUnopenedPairing(dir, false); err != nil {
		return fmt.Errorf("%s %w", errVaultSaveFailed, err)
	}
	return a.finishUnpair(ctrl, idx)
}

// sweepUnopenedPairing checks the vault plaintext folder of a pairing whose account could
// not be opened (desktop.Open failed while a config exists): no session can be open, but
// an earlier one may have left decrypted files behind.
func (a *App) sweepUnopenedPairing(dir string, force bool) ([]string, error) {
	profile, err := desktop.ProfileDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(desktop.DesktopConfigPath(profile))
	if err != nil || cfg.ServerURL == "" {
		return nil, nil // no pairing: nothing of it can be left
	}
	return sweepPairingLeftovers(cfg.ServerURL, profile, dir, force)
}

// UnpairResult tells the UI what a forced sign-out did. It is returned whole even when the
// sign-out stopped (Error set, SignedOut false): vaults closed before the failure have
// their changes in Recovered, and the user must learn where. A rejected Wails promise
// would lose that, so the failure travels in the result.
type UnpairResult struct {
	RecoveryDir string   `json:"recoveryDir"` // the recovery folder
	Recovered   []string `json:"recovered"`   // what was put there; empty when nothing
	SignedOut   bool     `json:"signedOut"`
	Error       string   `json:"error"` // "vault_recovery_failed: …" or another error; "" on success
}

// UnpairAnyway signs out although open vaults cannot be saved — the server is gone, the
// device was revoked — after the user confirmed it. Nothing unsaved is dropped: each open
// vault with changes is encrypted again locally into a vault folder in the recovery
// folder (its password opens it), or, without its keys, its decrypted files are moved
// there marked NOT ENCRYPTED; earlier sessions' decrypted leftovers are moved there too.
// Only then is the pairing removed. If keeping the work fails, nothing is signed out: the
// result carries "vault_recovery_failed: …" and lists the vaults that were closed (and
// kept) before the failure. The Go error is always nil; see UnpairResult.
func (a *App) UnpairAnyway() (UnpairResult, error) {
	a.accountMu.Lock()
	defer a.accountMu.Unlock()
	dir, err := recoveryDir()
	if err != nil {
		return UnpairResult{Error: err.Error()}, nil
	}
	res := UnpairResult{RecoveryDir: dir}
	fail := func(err error) (UnpairResult, error) {
		res.Error = fmt.Sprintf("%s %v", errVaultRecoveryFailed, err)
		return res, nil
	}
	ctrl, idx, _, ok := a.account()
	if ok {
		kept, err := forceCloseVaults(ctrl, dir)
		res.Recovered = append(res.Recovered, kept...)
		if err != nil {
			return fail(err)
		}
		more, err := sweepVaultLeftovers(ctrl, dir, true)
		res.Recovered = append(res.Recovered, more...)
		if err != nil {
			return fail(err)
		}
	} else {
		more, err := a.sweepUnopenedPairing(dir, true)
		res.Recovered = append(res.Recovered, more...)
		if err != nil {
			return fail(err)
		}
	}
	if err := a.finishUnpair(ctrl, idx); err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.SignedOut = true
	return res, nil
}

// finishUnpair is the part of a sign-out after the vaults: detach the mirror, revoke the
// device, and wipe the pairing and the profile's server state. Callers hold accountMu.
func (a *App) finishUnpair(ctrl *desktop.Controller, idx *index.Index) error {
	if a.mirror != nil {
		if err := a.mirror.Detach(); err != nil {
			return err
		}
	}
	profile, err := desktop.ProfileDir()
	if err != nil {
		return err
	}
	// End the device on the server too, or its token keeps working for anyone holding
	// a copy. Best effort: offline, unpairing still completes locally.
	if cfg, cerr := config.Load(desktop.DesktopConfigPath(profile)); cerr == nil && cfg.DeviceToken != "" {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		_ = protocol.NewUnscopedPinned(cfg.ServerURL, cfg.DeviceToken, cfg.ServerPin).RevokeDevice(ctx)
		cancel()
	}
	// Bound calls hold accountMu, so none is running now; downloads they started have
	// finished too, but wait on the controller anyway before its cache is wiped.
	if ctrl != nil {
		ctrl.Wait()
	}
	if idx != nil {
		_ = idx.Close() // release index.db so it can be deleted (Windows)
	}
	a.ctrl, a.idx, a.up, a.ready = nil, nil, nil, false
	a.urlMu.Lock()
	a.lastDAVURL = ""
	a.urlMu.Unlock()
	if err := os.Remove(desktop.DesktopConfigPath(profile)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return desktop.WipeState(profile)
}

// errVaultSaveFailed tags the error of an unpair stopped by a vault that could not be
// saved (or decrypted leftovers); the UI matches the tag, shows its own message and
// offers to sign out anyway. errVaultRecoveryFailed tags a forced sign-out that could not
// keep the unsaved work, and so did not sign out.
const (
	errVaultSaveFailed     = "vault_save_failed:"
	errVaultRecoveryFailed = "vault_recovery_failed:"
)

// closeOpenVaults saves and closes every open vault (desktop.Controller.CloseAllVaults: a
// nil error means every session was saved and its plaintext removed); tests replace it.
var closeOpenVaults = func(ctx context.Context, ctrl *desktop.Controller) error {
	return ctrl.CloseAllVaults(ctx)
}

// forceCloseVaults, sweepVaultLeftovers and recoveryDir are the vault steps of a sign-out;
// tests replace them.
var (
	forceCloseVaults = func(ctrl *desktop.Controller, dir string) ([]string, error) {
		return ctrl.ForceCloseAllVaults(dir)
	}
	sweepVaultLeftovers = func(ctrl *desktop.Controller, dir string, force bool) ([]string, error) {
		return ctrl.SweepVaultLeftovers(dir, force)
	}
	sweepPairingLeftovers = func(serverURL, profileDir, dir string, force bool) ([]string, error) {
		return desktop.SweepLeftoversFor(serverURL, profileDir, dir, "", force)
	}
	// recoveryDir is where a forced sign-out keeps unsaved vault work: a visible folder in
	// the home folder, so the user finds it without being told twice.
	recoveryDir = func() (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "DiscoDrive Recovery"), nil
	}
)
