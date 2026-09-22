package vaultmgr

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"discodrive.org/daemon/internal/vault"
)

func TestFinishCloseRetriesOnlyCleanup(t *testing.T) {
	encrypted, source, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "keep.txt"), []byte("saved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(source, encrypted); err != nil {
		t.Fatal(err)
	}
	m := &Manager{CacheRoot: cache}
	vi := VaultInfo{Name: "vault", Dir: encrypted}
	plain, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackChanges(vi); err != nil {
		t.Fatal(err)
	}
	p, err := m.PrepareClose(vi)
	if err != nil {
		t.Fatal(err)
	}
	m.AcceptClose(vi, p)
	calls := 0
	m.removePlaintext = func(root string) error {
		calls++
		// Model a partly successful RemoveAll followed by Finder recreating metadata.
		_ = os.Remove(filepath.Join(root, "contents", "keep.txt"))
		if err := os.WriteFile(filepath.Join(root, "contents", ".DS_Store"), []byte("Finder"), 0600); err != nil {
			return err
		}
		return syscall.ENOTEMPTY
	}
	if err := m.FinishClose(vi, p); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("got %v", err)
	}
	if !p.CleanupStarted() || calls != 3 {
		t.Fatalf("cleanup phase lost; attempts=%d", calls)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("public plaintext path still exposed: %v", err)
	}
	// Any attempt to prepare another delta from the partial tree must fail closed.
	if _, err := m.PrepareClose(vi); err == nil {
		t.Fatal("partial cleanup accepted as an edit")
	}
	m.removePlaintext = nil
	if err := m.FinishClose(vi, p); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if _, err := os.Stat(p.cleanupPath); !os.IsNotExist(err) {
		t.Fatalf("plaintext survived: %v", err)
	}
	if _, err := m.PrepareClose(vi); !errors.Is(err, ErrLocked) {
		t.Fatalf("keys survived successful cleanup: %v", err)
	}
}

func TestFinderMetadataDoesNotChangeVault(t *testing.T) {
	encrypted, source, cache := t.TempDir(), t.TempDir(), t.TempDir()
	v, err := vault.Create(encrypted, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "keep.txt"), []byte("saved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := v.EncryptTree(source, encrypted); err != nil {
		t.Fatal(err)
	}
	m := &Manager{CacheRoot: cache}
	vi := VaultInfo{Name: "vault", Dir: encrypted}
	plain, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.TrackChanges(vi); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, ".DS_Store"), []byte("Finder"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := m.PrepareClose(vi)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != encrypted {
		t.Fatal("Finder metadata triggered encryption")
	}
	if err := m.FinishClose(vi, p); err != nil {
		t.Fatal(err)
	}
}
