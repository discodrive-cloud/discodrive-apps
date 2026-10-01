package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/index"
)

func TestLegacyFolderIgnoresFinderMetadata(t *testing.T) {
	root, err := legacyVaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(root, ".DS_Store")
	t.Cleanup(func() { os.Remove(metadata) })
	if err := os.WriteFile(metadata, []byte("Finder metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := LegacyVaultFolder(); err != nil || p != "" {
		t.Fatalf("empty legacy cache reported as unsaved work: %q %v", p, err)
	}
}

func TestSweepKeepsOtherActiveSession(t *testing.T) {
	b, _, plain, _ := openCloseFixture(t)
	if err := os.WriteFile(filepath.Join(plain, "new.txt"), []byte("B unsaved work"), 0600); err != nil {
		t.Fatal(err)
	}
	cipher := b.sessions["vault"].tmpDir
	a := &Controller{vaultPlainRoot: filepath.Join(t.TempDir(), "A"), tempRoot: b.tempRoot}
	if _, err := a.SweepVaultLeftovers(t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cipher); err != nil {
		t.Errorf("sign-out A removed ACTIVE session B ciphertext: %v", err)
	}
	if err := b.CloseAllVaults(context.Background()); err != nil {
		t.Errorf("B can no longer save: %v", err)
	}
}

// The old folder identity omitted the server/account. Even an index correctly
// bound to the current server must not import a previous account's private files.
func TestOpenDoesNotImportLegacyFromPreviousAccount(t *testing.T) {
	profile := t.TempDir()
	cfg := config.Config{ServerURL: "https://new-account.example", DeviceToken: "new-device"}
	if err := SaveConfig(profile, cfg); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(IndexDBPath(profile))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.SetServerURL(cfg.ServerURL); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(index.Node{NodeID: "new-account-vault", RelPath: "Secrets", IsDir: true}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := legacyVaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(ContentDir(profile) + "\nSecrets"))
	name := "Secrets-" + hex.EncodeToString(sum[:4])
	old := filepath.Join(root, name)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(old) })
	if err := os.WriteFile(filepath.Join(old, "private.txt"), []byte("previous account"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, opened, err := Open(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if _, err := os.Stat(filepath.Join(c.vaultPlainRoot, name)); !os.IsNotExist(err) {
		t.Fatalf("previous account's files imported: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(old, "private.txt")); err != nil || string(b) != "previous account" {
		t.Fatalf("legacy source changed: %q %v", b, err)
	}
	if reported, err := LegacyVaultFolder(); err != nil || reported != root {
		t.Fatalf("legacy source not reported to the user: %q %v", reported, err)
	}
}
func TestSweepDetectsLegacyPlaintext(t *testing.T) {
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	oldroot := filepath.Join(cache, "discodrive", "open")
	_, _, plain, _ := openCloseFixtureRoot(t, oldroot)
	defer os.RemoveAll(plain)
	if err := os.WriteFile(filepath.Join(plain, "private.txt"), []byte("unsaved legacy plaintext"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = SweepLeftoversFor("https://review.invalid", t.TempDir(), t.TempDir(), t.TempDir(), false)
	if !errors.Is(err, ErrVaultLeftovers) {
		t.Fatalf("pre-upgrade plaintext remains but sign-out succeeds: err=%v", err)
	}
}

func TestForcedSweepNeverMovesUnidentifiedLegacyFiles(t *testing.T) {
	root, err := legacyVaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "unidentified")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	file := filepath.Join(other, "note.txt")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	recovery := t.TempDir()
	if _, err := SweepLeftoversFor("https://example.test", t.TempDir(), recovery, t.TempDir(), true); !errors.Is(err, ErrVaultLeftovers) {
		t.Fatalf("expected explicit legacy warning: %v", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "keep" {
		t.Fatalf("unidentified work touched: %q %v", b, err)
	}
	if entries, err := os.ReadDir(recovery); err != nil || len(entries) != 0 {
		t.Fatalf("unexpected recovery: %v %v", entries, err)
	}
}
