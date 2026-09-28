package vaultmgr_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

func newManager(t *testing.T) *vaultmgr.Manager {
	t.Helper()
	return &vaultmgr.Manager{
		SyncDir:   t.TempDir(),
		CacheRoot: t.TempDir(),
	}
}

// Test 1: Detect finds vaults and ignores non-vaults
func TestDetect(t *testing.T) {
	m := newManager(t)

	// Create a vault
	vi, err := m.Create("Сейф", "pw")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Create a plain non-vault folder
	if err := os.Mkdir(filepath.Join(m.SyncDir, "НеСейф"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a file (not a folder) — should be ignored
	if err := os.WriteFile(filepath.Join(m.SyncDir, "файл.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	vaults, err := m.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	if len(vaults) != 1 {
		t.Fatalf("Detect: expected 1 vault, got %d: %+v", len(vaults), vaults)
	}
	if vaults[0].Name != "Сейф" {
		t.Errorf("Name: expected %q, got %q", "Сейф", vaults[0].Name)
	}
	if vaults[0].Dir != vi.Dir {
		t.Errorf("Dir: expected %q, got %q", vi.Dir, vaults[0].Dir)
	}
	// Verify that Dir is inside SyncDir
	if !strings.HasPrefix(vaults[0].Dir, m.SyncDir) {
		t.Errorf("Dir %q is not under SyncDir %q", vaults[0].Dir, m.SyncDir)
	}
}

// Test 2: Full round-trip: Create → Open → write file → Close (no password) → verify wipe → Open → file intact
func TestOpenCloseRoundTrip(t *testing.T) {
	m := newManager(t)

	// Create vault
	vi, err := m.Create("vault1", "секрет")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Open
	pd, err := m.Open(vi, "секрет")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write a file into plaintext
	secretPath := filepath.Join(pd, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("тайна"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Close WITHOUT password — keys come from memory
	if err := m.Close(vi); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Plaintext folder must be WIPED
	if _, err := os.Stat(pd); !os.IsNotExist(err) {
		t.Errorf("plainDir %q must be removed after Close, but os.Stat returned: %v", pd, err)
	}

	// d/ must exist in the vault
	dPath := filepath.Join(vi.Dir, "d")
	if _, err := os.Stat(dPath); os.IsNotExist(err) {
		t.Errorf("the d/ folder must exist in the vault after Close")
	}

	// Open again
	pd2, err := m.Open(vi, "секрет")
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}

	// File must be present with the same content
	got, err := os.ReadFile(filepath.Join(pd2, "secret.txt"))
	if err != nil {
		t.Fatalf("ReadFile after the second Open: %v", err)
	}
	if string(got) != "тайна" {
		t.Errorf("content: expected %q, got %q", "тайна", string(got))
	}
}

// Test 3: SAFETY — plainDir is under CacheRoot and NOT under SyncDir
func TestSafetyPlainDirOutsideSyncDir(t *testing.T) {
	syncDir := t.TempDir()
	cacheRoot := t.TempDir()

	m := &vaultmgr.Manager{
		SyncDir:   syncDir,
		CacheRoot: cacheRoot,
	}

	// Create vault and open
	vi, err := m.Create("test-vault", "pw")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	pd, err := m.Open(vi, "pw")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Core guarantee: plainDir is rooted under CacheRoot
	if !strings.HasPrefix(pd, cacheRoot) {
		t.Errorf("SAFETY VIOLATION: plainDir %q is not under CacheRoot %q", pd, cacheRoot)
	}

	// Core guarantee: plainDir is NOT under SyncDir
	if strings.HasPrefix(pd, syncDir) {
		t.Errorf("SAFETY VIOLATION: plainDir %q is under SyncDir %q — plaintext would end up in sync!", pd, syncDir)
	}

	// Additional check: SyncDir and CacheRoot do not overlap
	if strings.HasPrefix(cacheRoot, syncDir) || strings.HasPrefix(syncDir, cacheRoot) {
		t.Errorf("SAFETY: CacheRoot and SyncDir overlap: %q / %q", cacheRoot, syncDir)
	}
}

// Test 4: Wrong password is caught on Open; no plaintext is created, no keys stored.
// Subsequent Close(vi) without password returns ErrLocked (does not attempt to encrypt garbage).
func TestWrongPasswordDoesNotWipePlaintext(t *testing.T) {
	m := newManager(t)

	// Create vault with the correct password
	vi, err := m.Create("секретный", "правильный")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Try Open with the WRONG password — must return ErrWrongPassword
	_, err = m.Open(vi, "неверный")
	if err == nil {
		t.Fatal("Open with a wrong password must return an error")
	}
	if !errors.Is(err, vault.ErrWrongPassword) {
		t.Errorf("expected vault.ErrWrongPassword from Open, got: %v", err)
	}

	// No plaintext folder may exist — a failed Open does not create one.
	if left, _ := os.ReadDir(m.CacheRoot); len(left) != 0 {
		t.Errorf("CacheRoot must stay empty after a wrong Open, found %v", left)
	}

	// Close without password must return ErrLocked (no keys in memory)
	err = m.Close(vi)
	if !errors.Is(err, vaultmgr.ErrLocked) {
		t.Errorf("expected ErrLocked after a failed Open, got: %v", err)
	}
}

// Test 5: Close without a prior Open in memory → ErrLocked, plainDir intact.
func TestCloseWithoutOpenInMemory(t *testing.T) {
	// Create Manager 1, open vault
	m1 := newManager(t)
	vi, err := m1.Create("сейф", "pw")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	pd, err := m1.Open(vi, "pw")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Write a file
	if err := os.WriteFile(filepath.Join(pd, "keep.txt"), []byte("держи"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create Manager 2 with the same paths — no keys in memory (simulates a crash / new process)
	m2 := &vaultmgr.Manager{
		SyncDir:   m1.SyncDir,
		CacheRoot: m1.CacheRoot,
	}

	// Close via m2 must return ErrLocked
	err = m2.Close(vi)
	if !errors.Is(err, vaultmgr.ErrLocked) {
		t.Fatalf("expected ErrLocked from a new Manager, got: %v", err)
	}

	// plainDir must be INTACT — no data lost
	if _, statErr := os.Stat(pd); statErr != nil {
		t.Errorf("plainDir %q must remain intact after ErrLocked, but: %v", pd, statErr)
	}
	got, err := os.ReadFile(filepath.Join(pd, "keep.txt"))
	if err != nil {
		t.Errorf("file keep.txt disappeared: %v", err)
	} else if string(got) != "держи" {
		t.Errorf("content changed: %q", string(got))
	}
}

// Test 6: Create on an existing folder → error
func TestCreateExistingFolder(t *testing.T) {
	m := newManager(t)

	// Pre-create the folder
	existing := filepath.Join(m.SyncDir, "уже-есть")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := m.Create("уже-есть", "pw")
	if err == nil {
		t.Fatal("Create for an existing folder must return an error")
	}
}
