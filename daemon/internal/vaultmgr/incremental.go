package vaultmgr

import (
	"fmt"
	"os"
	"path/filepath"

	"discodrive.org/daemon/internal/vault"
)

// PreparedClose retains both keys and plaintext until the caller commits its
// encrypted tree to remote storage. The caller owns Dir if it differs from vi.Dir.
type PreparedClose struct {
	Dir         string
	snapshot    *vault.TreeSnapshot
	cleanupPath string
}

// TrackChanges binds the just-decrypted files to their encrypted representation.
func (m *Manager) TrackChanges(vi VaultInfo) error {
	m.mu.Lock()
	v := m.unlocked[vi.Name]
	m.mu.Unlock()
	if v == nil {
		return ErrLocked
	}
	s, err := v.SnapshotTree(m.plainDir(vi.Name), vi.Dir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshots == nil {
		m.snapshots = map[string]*vault.TreeSnapshot{}
	}
	m.snapshots[vi.Name] = s
	return nil
}

func (m *Manager) PrepareClose(vi VaultInfo) (*PreparedClose, error) {
	m.mu.Lock()
	v, s := m.unlocked[vi.Name], m.snapshots[vi.Name]
	m.mu.Unlock()
	if v == nil {
		return nil, ErrLocked
	}
	if s == nil {
		return nil, fmt.Errorf("vault: missing opening snapshot")
	}
	dir, after, err := v.PrepareTree(m.plainDir(vi.Name), vi.Dir, s)
	if err != nil {
		return nil, err
	}
	return &PreparedClose{Dir: dir, snapshot: after}, nil
}

// AcceptClose advances the baseline after the remote commit, even if the user
// edited plaintext during upload. Such edits are preserved for the next close.
func (m *Manager) AcceptClose(vi VaultInfo, p *PreparedClose) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshots[vi.Name] = p.snapshot
}

// CleanupStarted distinguishes a retryable cleanup from an editable open tree.
func (p *PreparedClose) CleanupStarted() bool { return p.cleanupPath != "" }

func (m *Manager) FinishClose(vi VaultInfo, p *PreparedClose) error {
	if !p.CleanupStarted() {
		same, err := p.snapshot.MatchesPlain(m.plainDir(vi.Name))
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("vault: files changed while saving; close the vault again to save them")
		}
		// Detach the committed tree from the public Finder path before removing files.
		// A failed RemoveAll must never become the input of another upload delta.
		cleanup, err := os.MkdirTemp(m.CacheRoot, ".closing-")
		if err != nil {
			return err
		}
		if err := os.Rename(m.plainDir(vi.Name), filepath.Join(cleanup, "contents")); err != nil {
			os.Remove(cleanup)
			return err
		}
		p.cleanupPath = cleanup
	}
	remove := m.removePlaintext
	if remove == nil {
		remove = os.RemoveAll
	}
	// Finder may write .DS_Store once more through an already open directory handle.
	// Keep the cleanup phase even if all attempts fail; retries never re-upload it.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = remove(p.cleanupPath); err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.unlocked, vi.Name)
	delete(m.snapshots, vi.Name)
	return nil
}
