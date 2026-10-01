package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
)

// unpairProfile makes a paired profile under a temporary home: a config without a device
// token (so nothing is revoked over the network) and an index file.
func unpairProfile(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	profile, err := desktop.ProfileDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(profile, home) {
		t.Fatalf("profile %s is not under the test home: refusing to touch it", profile)
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (config.Config{ServerURL: "https://example.invalid"}).Save(desktop.DesktopConfigPath(profile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(desktop.IndexDBPath(profile), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	return profile
}

// An open vault that cannot be saved stops the unpair: the session, its plaintext, the
// pairing and the index are left as they were, and the error says why, tagged for the UI.
func TestUnpairStopsWhenAVaultCannotBeSaved(t *testing.T) {
	profile := unpairProfile(t)
	stubVaultOps(t, errors.New("vault: files changed while saving; close the vault again to save them"), false, nil, nil)
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	err := a.Unpair()
	if err == nil || !strings.HasPrefix(err.Error(), "vault_save_failed:") {
		t.Fatalf("Unpair = %v, want a vault_save_failed: error", err)
	}
	for _, p := range []string{desktop.DesktopConfigPath(profile), desktop.IndexDBPath(profile)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed by a failed unpair: %v", p, err)
		}
	}
	if !a.ready || a.ctrl == nil {
		t.Error("the account was dropped by a failed unpair")
	}
}

// With every vault saved, unpair goes on as before.
func TestUnpairProceedsWhenVaultsSaved(t *testing.T) {
	profile := unpairProfile(t)
	calls := stubVaultOps(t, nil, false, nil, nil)
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	if err := a.Unpair(); err != nil {
		t.Fatalf("Unpair: %v", err)
	}
	if strings.Join(*calls, ",") != "close,sweep false" {
		t.Errorf("calls = %v, want the vaults closed and leftovers checked", *calls)
	}
	if _, err := os.Stat(desktop.DesktopConfigPath(profile)); !os.IsNotExist(err) {
		t.Errorf("config kept after unpair: %v", err)
	}
	if a.ready {
		t.Error("still paired")
	}
}

// stubVaultOps replaces every vault operation of a sign-out, so no test touches the real
// cache or temp folders.
func stubVaultOps(t *testing.T, closeErr error, leftovers bool, forced []string, forceErr error) *[]string {
	t.Helper()
	oc, of, os_, or, op := closeOpenVaults, forceCloseVaults, sweepVaultLeftovers, recoveryDir, sweepPairingLeftovers
	t.Cleanup(func() {
		closeOpenVaults, forceCloseVaults, sweepVaultLeftovers, recoveryDir, sweepPairingLeftovers = oc, of, os_, or, op
	})
	var calls []string
	closeOpenVaults = func(context.Context, *desktop.Controller) error { calls = append(calls, "close"); return closeErr }
	forceCloseVaults = func(_ *desktop.Controller, dir string) ([]string, error) {
		calls = append(calls, "force "+dir)
		return forced, forceErr
	}
	sweepVaultLeftovers = func(_ *desktop.Controller, dir string, force bool) ([]string, error) {
		calls = append(calls, fmt.Sprintf("sweep %v", force))
		if leftovers && !force {
			return nil, desktop.ErrVaultLeftovers
		}
		return nil, nil
	}
	recoveryDir = func() (string, error) { return "/recovery", nil }
	sweepPairingLeftovers = func(serverURL, _ string, dir string, force bool) ([]string, error) {
		calls = append(calls, fmt.Sprintf("sweep pairing %s %v", serverURL, force))
		if leftovers && !force {
			return nil, desktop.ErrVaultLeftovers
		}
		return nil, nil
	}
	return &calls
}

// Decrypted leftovers of an earlier session stop an ordinary sign-out too.
func TestUnpairStopsOnVaultLeftovers(t *testing.T) {
	profile := unpairProfile(t)
	stubVaultOps(t, nil, true, nil, nil)
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	err := a.Unpair()
	if err == nil || !strings.HasPrefix(err.Error(), "vault_save_failed:") {
		t.Fatalf("Unpair = %v, want vault_save_failed:", err)
	}
	if _, err := os.Stat(desktop.DesktopConfigPath(profile)); err != nil {
		t.Errorf("config removed: %v", err)
	}
}

// "Sign out anyway": the vaults are closed by force into the recovery folder, leftovers
// are moved there too, and the sign-out completes, telling where the work was kept.
func TestUnpairAnywayKeepsUnsavedWork(t *testing.T) {
	profile := unpairProfile(t)
	calls := stubVaultOps(t, errors.New("server gone"), true, []string{"/recovery/Secrets 2026-10-01 10.00.00"}, nil)
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	res, err := a.UnpairAnyway()
	if err != nil {
		t.Fatalf("UnpairAnyway: %v", err)
	}
	if res.RecoveryDir != "/recovery" || len(res.Recovered) != 1 || !res.SignedOut || res.Error != "" {
		t.Errorf("result = %+v", res)
	}
	if strings.Join(*calls, ",") != "force /recovery,sweep true" {
		t.Errorf("calls = %v", *calls)
	}
	if _, err := os.Stat(desktop.DesktopConfigPath(profile)); !os.IsNotExist(err) {
		t.Errorf("config kept: %v", err)
	}
	if a.ready {
		t.Error("still paired")
	}
}

// If even the recovery fails (say, the disk is full), nothing is signed out.
func TestUnpairAnywayStopsWhenRecoveryFails(t *testing.T) {
	profile := unpairProfile(t)
	stubVaultOps(t, nil, false, nil, errors.New("no space left on device"))
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	res, err := a.UnpairAnyway()
	if err != nil || !strings.HasPrefix(res.Error, "vault_recovery_failed:") || res.SignedOut {
		t.Fatalf("UnpairAnyway = %+v, %v; want vault_recovery_failed: in the result, still signed in", res, err)
	}
	if _, err := os.Stat(desktop.DesktopConfigPath(profile)); err != nil {
		t.Errorf("config removed: %v", err)
	}
	if !a.ready {
		t.Error("signed out although the recovery failed")
	}
}

// m-3: vaults kept before a later one failed are reported with the error, so the user
// learns where their changes went although the device stays signed in.
func TestUnpairAnywayPartialReportsKept(t *testing.T) {
	unpairProfile(t)
	stubVaultOps(t, nil, false, []string{"/recovery/A 2026-10-01 10.00.00"}, errors.New("vault B: files changed while saving"))
	a := &App{ctx: context.Background(), ctrl: &desktop.Controller{}, ready: true}
	res, err := a.UnpairAnyway()
	if err != nil || res.SignedOut || len(res.Recovered) != 1 || !strings.HasPrefix(res.Error, "vault_recovery_failed:") {
		t.Fatalf("UnpairAnyway = %+v, %v", res, err)
	}
}

// m-4: an account that never opened still has its pairing's plaintext folder checked.
func TestUnpairNotReadySweepsPairingLeftovers(t *testing.T) {
	profile := unpairProfile(t)
	calls := stubVaultOps(t, nil, true, nil, nil)
	a := &App{ctx: context.Background()}
	if err := a.Unpair(); err == nil || !strings.HasPrefix(err.Error(), "vault_save_failed:") {
		t.Fatalf("Unpair = %v, want vault_save_failed:", err)
	}
	if _, err := os.Stat(desktop.DesktopConfigPath(profile)); err != nil {
		t.Errorf("config removed: %v", err)
	}
	res, err := a.UnpairAnyway()
	if err != nil || !res.SignedOut {
		t.Fatalf("UnpairAnyway = %+v, %v", res, err)
	}
	want := "sweep pairing https://example.invalid false,sweep pairing https://example.invalid true"
	if strings.Join(*calls, ",") != want {
		t.Errorf("calls = %v, want %s", *calls, want)
	}
}
