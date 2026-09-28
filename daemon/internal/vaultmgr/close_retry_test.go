package vaultmgr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCloseRetryAfterPlaintextChanges(t *testing.T) {
	m := &Manager{SyncDir: t.TempDir(), CacheRoot: t.TempDir()}
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(pd, "note.txt"), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("first edit")
	v := m.unlocked[vi.Name]
	stage, after, err := v.PrepareTreeIn(pd, vi.Dir, m.snapshots[vi.Name], m.CacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if err := applyStage(stage, vi.Dir, m.liveAtOpen[vi.Name]); err != nil {
		t.Fatal(err)
	}
	m.snapshots[vi.Name] = after
	write("second edit while saving")
	if err := m.FinishClose(vi, &PreparedClose{Dir: vi.Dir, snapshot: after}); err == nil {
		t.Fatal("concurrent edit was not detected")
	}
	if err := m.Close(vi); err != nil {
		t.Fatalf("retry: %v", err)
	}
	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pd, "note.txt"))
	if err != nil || string(b) != "second edit while saving" {
		t.Fatalf("saved %q: %v", b, err)
	}
}

func TestCloseRetriesFailedCleanup(t *testing.T) {
	m := &Manager{SyncDir: t.TempDir(), CacheRoot: t.TempDir()}
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pd, "secret.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	m.removePlaintext = func(string) error { return errors.New("cleanup unavailable") }
	if err = m.Close(vi); err == nil {
		t.Fatal("expected cleanup failure")
	}
	if !m.IsOpen(vi) {
		t.Fatal("cleanup must remain available from the tray")
	}
	if _, err := m.Open(vi, "pw"); err == nil {
		t.Fatal("reopened before cleanup finished")
	}
	m.removePlaintext = nil
	if err = m.Close(vi); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(m.CacheRoot, ".closing-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("plaintext remains: %v %v", leftovers, err)
	}
	pd, err = m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(pd, "secret.txt"))
	if err != nil || string(b) != "secret" {
		t.Fatalf("saved %q: %v", b, err)
	}
}

// A cleanup that failed and was never retried (the process quit or crashed) leaves the
// already-saved plaintext in a hidden .closing-* folder. The next open removes it.
func TestOpenRemovesCleanupLeftAfterRestart(t *testing.T) {
	m := &Manager{SyncDir: t.TempDir(), CacheRoot: t.TempDir()}
	vi, err := m.Create("v", "pw")
	if err != nil {
		t.Fatal(err)
	}
	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pd, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.removePlaintext = func(string) error { return errors.New("busy") }
	if err := m.Close(vi); err == nil {
		t.Fatal("expected cleanup failure")
	}
	restarted := &Manager{SyncDir: m.SyncDir, CacheRoot: m.CacheRoot}
	pd, err = restarted.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(m.CacheRoot, ".closing-*")); len(left) != 0 {
		t.Fatalf("decrypted copy left on disk: %v", left)
	}
	if b, err := os.ReadFile(filepath.Join(pd, "secret.txt")); err != nil || string(b) != "secret" {
		t.Fatalf("saved file: %q %v", b, err)
	}
}
