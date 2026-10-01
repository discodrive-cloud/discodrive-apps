package vaultmgr

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"discodrive.org/daemon/internal/vault"
)

// Two "Close vault" clicks in the tray run two handlers at once. The second must be
// turned away while the first is still working, not race it on the vault's state.
func TestConcurrentCloseOneWinsOtherBusy(t *testing.T) {
	sync_, cache := t.TempDir(), t.TempDir()
	m := &Manager{SyncDir: sync_, CacheRoot: cache, unlocked: map[string]*vault.Vault{}}
	vi, err := m.Create("vault", "pw")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "a.txt"), []byte("edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.removePlaintext = func(root string) error {
		once.Do(func() { close(entered) })
		<-release
		return os.RemoveAll(root)
	}

	errs := make(chan error, 2)
	go func() { errs <- m.Close(vi) }()
	<-entered
	second := make(chan error, 1)
	go func() { second <- m.Close(vi) }()
	select {
	case err := <-second:
		errs <- err
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("second Close ran alongside the first instead of returning ErrVaultBusy")
	}
	close(release)
	var ok, busy int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrVaultBusy):
			busy++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || busy != 1 {
		t.Fatalf("ok=%d busy=%d, want 1 and 1", ok, busy)
	}
	if m.IsOpen(vi) {
		t.Fatal("vault still open after the winning close")
	}
}

// Opening while a close of the same vault runs is refused the same way.
func TestOpenDuringCloseIsBusy(t *testing.T) {
	m := &Manager{SyncDir: t.TempDir(), CacheRoot: t.TempDir(), unlocked: map[string]*vault.Vault{}}
	vi, err := m.Create("vault", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(vi, "pw"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	m.removePlaintext = func(root string) error {
		close(entered)
		<-release
		return os.RemoveAll(root)
	}
	done := make(chan error, 1)
	go func() { done <- m.Close(vi) }()
	<-entered
	if _, err := m.Open(vi, "pw"); !errors.Is(err, ErrVaultBusy) {
		t.Fatalf("Open during Close = %v, want ErrVaultBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCreateRejectsPathNames(t *testing.T) {
	root := t.TempDir()
	syncDir := filepath.Join(root, "sync")
	if err := os.Mkdir(syncDir, 0o700); err != nil {
		t.Fatal(err)
	}
	m := &Manager{SyncDir: syncDir, CacheRoot: t.TempDir()}
	for _, name := range []string{"../x", "a/b", `a\b`, "..", ".", "", "x\x00y"} {
		if _, err := m.Create(name, "pw"); err == nil {
			t.Errorf("Create(%q) accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "x")); !os.IsNotExist(err) {
		t.Fatal("Create(../x) made a vault outside SyncDir")
	}
}
