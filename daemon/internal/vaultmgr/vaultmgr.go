package vaultmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"discodrive.org/daemon/internal/vault"
)

const markerFile = "vault.cryptomator"

// ErrLocked is returned when vault keys are not in memory (Open must be called first).
var ErrLocked = errors.New("vault is not unlocked in memory (open it again)")

// VaultInfo describes a detected vault.
type VaultInfo struct {
	Name string // vault folder name
	Dir  string // absolute path of the vault folder (inside SyncDir)
}

// Manager manages vaults in SyncDir; plaintext of open vaults is kept in CacheRoot (OUTSIDE SyncDir).
type Manager struct {
	SyncDir   string
	CacheRoot string // e.g. <UserCacheDir>/discodrive/open — MUST be outside SyncDir

	mu              sync.Mutex
	snapshots       map[string]*vault.TreeSnapshot
	removePlaintext func(string) error      // nil uses os.RemoveAll; injectable for cleanup fault tests
	unlocked        map[string]*vault.Vault // vault name → keys (while open)
}

// New creates a Manager with CacheRoot in the system cache directory (outside SyncDir).
func New(syncDir string) (*Manager, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Manager{
		SyncDir:   syncDir,
		CacheRoot: filepath.Join(cache, "discodrive", "open"),
		unlocked:  map[string]*vault.Vault{},
	}, nil
}

// Detect scans SyncDir (one level deep) for vault folders (containing vault.cryptomator).
func (m *Manager) Detect() ([]VaultInfo, error) {
	entries, err := os.ReadDir(m.SyncDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []VaultInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.SyncDir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, markerFile)); err == nil {
			out = append(out, VaultInfo{Name: e.Name(), Dir: dir})
		}
	}
	return out, nil
}

// plainDir returns the decrypted plaintext path for the vault (OUTSIDE SyncDir).
func (m *Manager) plainDir(name string) string { return filepath.Join(m.CacheRoot, name) }

// IsOpen reports whether the vault is open (its plaintext folder exists).
func (m *Manager) IsOpen(name string) bool {
	_, err := os.Stat(m.plainDir(name))
	return err == nil
}

// Create creates a new vault named name in SyncDir.
func (m *Manager) Create(name, password string) (VaultInfo, error) {
	dir := filepath.Join(m.SyncDir, name)
	if _, err := os.Stat(dir); err == nil {
		return VaultInfo{}, fmt.Errorf("directory %q already exists", name)
	}
	if _, err := vault.Create(dir, password); err != nil {
		return VaultInfo{}, err
	}
	return VaultInfo{Name: name, Dir: dir}, nil
}

// Open decrypts the vault into plainDir (outside SyncDir) and returns the plaintext path.
// Wrong password → vault.ErrWrongPassword (no plaintext created, no keys stored).
// If the vault is empty (no d/), an empty plainDir is created without calling DecryptTree.
// On success, keys are stored in memory — Close will not require the password.
func (m *Manager) Open(vi VaultInfo, password string) (string, error) {
	v, err := vault.Open(vi.Dir, password)
	if err != nil {
		return "", err
	}
	return m.openUnlocked(vi, v)
}

// OpenWithKeys opens a vault using master keys recovered from a recovery phrase,
// bypassing the password.
func (m *Manager) OpenWithKeys(vi VaultInfo, encKey, macKey []byte) (string, error) {
	v, err := vault.OpenWithKeys(vi.Dir, encKey, macKey)
	if err != nil {
		return "", err
	}
	return m.openUnlocked(vi, v)
}

// openUnlocked decrypts the (already-unlocked) vault into plainDir and stores its keys.
func (m *Manager) openUnlocked(vi VaultInfo, v *vault.Vault) (string, error) {
	pd := m.plainDir(vi.Name)
	if err := os.MkdirAll(pd, 0o700); err != nil {
		return "", err
	}
	// Empty vault (no d/) — nothing to decrypt, plainDir was already created
	if _, err := os.Stat(filepath.Join(vi.Dir, "d")); os.IsNotExist(err) {
		m.storeUnlocked(vi.Name, v)
		return pd, nil
	}
	if err := v.DecryptTree(vi.Dir, pd); err != nil {
		return "", err
	}
	m.storeUnlocked(vi.Name, v)
	return pd, nil
}

// Close re-encrypts the plaintext back into the vault using in-memory keys, then wipes plainDir.
// If keys are not in memory (failed Open, crash, new process) — returns ErrLocked; plaintext is untouched.
func (m *Manager) Close(vi VaultInfo) error {
	m.mu.Lock()
	v, ok := m.unlocked[vi.Name]
	m.mu.Unlock()

	if !ok {
		return ErrLocked
	}

	pd := m.plainDir(vi.Name)
	if _, err := os.Stat(pd); err != nil {
		return fmt.Errorf("vault %q is not open", vi.Name)
	}

	// Remove old d/ (ciphertext), write new one from plaintext;
	// masterkey.cryptomator and vault.cryptomator are left intact
	if err := os.RemoveAll(filepath.Join(vi.Dir, "d")); err != nil {
		return err
	}
	if err := v.EncryptTree(pd, vi.Dir); err != nil {
		return err
	}
	// Wipe plaintext only after successful re-encryption
	if err := os.RemoveAll(pd); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.unlocked, vi.Name)
	delete(m.snapshots, vi.Name)
	m.mu.Unlock()

	return nil
}

// storeUnlocked stores keys under the mutex with lazy map initialisation.
func (m *Manager) storeUnlocked(name string, v *vault.Vault) {
	m.mu.Lock()
	if m.unlocked == nil {
		m.unlocked = map[string]*vault.Vault{}
	}
	m.unlocked[name] = v
	m.mu.Unlock()
}

// Orphans returns names of orphaned plaintext folders in CacheRoot (left over from a crash with open vaults).
func (m *Manager) Orphans() ([]string, error) {
	entries, err := os.ReadDir(m.CacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
