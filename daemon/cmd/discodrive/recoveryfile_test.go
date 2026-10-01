package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRecoveryFilePath(t *testing.T) {
	cfg, sync := t.TempDir(), t.TempDir()
	p, err := recoveryFilePath(cfg, sync, "Secrets")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg, "discodrive", "Secrets-recovery.txt"); p != want {
		t.Fatalf("path = %q, want %q", p, want)
	}
	for _, bad := range []string{"../x", "a/b", `a\b`, "", ".", ".."} {
		if _, err := recoveryFilePath(cfg, sync, bad); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

// Pairing with --dir ~ makes the home folder the sync root; a config dir under it would
// be uploaded, so the file is refused rather than written there.
func TestRecoveryFilePathNeverInsideSyncDir(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "Library", "Application Support")
	if _, err := recoveryFilePath(cfg, home, "v"); !errors.Is(err, errRecoveryInSyncDir) {
		t.Fatalf("got %v, want errRecoveryInSyncDir", err)
	}
}

func TestSaveRecoveryPhrasePrivate(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)
	t.Setenv("AppData", cfg)
	p, err := saveRecoveryPhrase(t.TempDir(), "v", "words words")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "words words\n" {
		t.Fatalf("content %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
}
