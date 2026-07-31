package desktop

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"discodrive.org/daemon/internal/safepath"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

// vaultSession records the open state of a decrypted vault so CloseVault can
// re-encrypt the plaintext and upload it back to the server.
type vaultSession struct {
	vm       *vaultmgr.Manager
	vi       vaultmgr.VaultInfo
	tmpDir   string // local ciphertext dir (downloaded from server; re-encrypted into on Close)
	relPath  string // server-relative vault folder
	plainDir string // decrypted plaintext dir (for idempotent re-open)
}

// VaultRef identifies a vault discovered on the server.
type VaultRef struct {
	Name    string // folder name
	RelPath string // server-relative path of the vault folder
}

// CreateVault creates an encrypted Cryptomator vault locally and uploads its whole
// ciphertext tree to the server under parentRelPath/name (parentRelPath "" = root).
// Returns the vault's recovery phrase (44 words) so the UI can show it to the user —
// it is the only way back in if the password is lost.
func (c *Controller) CreateVault(ctx context.Context, parentRelPath, name, password string) (string, error) {
	tmp, err := os.MkdirTemp("", "ddvault-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	v, err := vault.Create(tmp, password)
	if err != nil {
		return "", err
	}
	phrase := v.RecoveryKey()

	serverBase := name
	if parentRelPath != "" {
		serverBase = parentRelPath + "/" + name
	}
	if err := c.uploadTree(ctx, tmp, serverBase); err != nil {
		return "", err
	}
	// Pull the just-uploaded vault into the local index so it shows up in the tree
	// and ListVaults immediately, without the user pressing Refresh.
	_, err = c.Refresh(ctx)
	return phrase, err
}

// CloseAllVaults re-encrypts and uploads every open vault, ending all sessions. Used
// when leaving the vaults view or quitting so plaintext never lingers on disk and
// pending changes are saved. Returns the first error encountered.
func (c *Controller) CloseAllVaults(ctx context.Context) error {
	c.mu.Lock()
	rels := make([]string, 0, len(c.sessions))
	for r := range c.sessions {
		rels = append(rels, r)
	}
	c.mu.Unlock()

	var firstErr error
	for _, r := range rels {
		if err := c.CloseVault(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// uploadTree walks localRoot and recreates it on the server under serverBase using
// EnsureDir (directories) and PushFile (files, baseVersion nil).
func (c *Controller) uploadTree(ctx context.Context, localRoot, serverBase string) error {
	return filepath.Walk(localRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			// Root itself — ensure the base directory exists on the server.
			_, err := c.srv.EnsureDir(ctx, serverBase)
			return err
		}
		if info.IsDir() {
			_, err := c.srv.EnsureDir(ctx, serverBase+"/"+rel)
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, _, err = c.srv.PushFile(ctx, serverBase+"/"+rel, nil, f, info.ModTime())
		return err
	})
}

// ListVaults scans the local index for "vault.cryptomator" markers and returns the
// parent folder of each as a vault.
func (c *Controller) ListVaults() ([]VaultRef, error) {
	nodes, err := c.idx.All()
	if err != nil {
		return nil, err
	}
	var out []VaultRef
	for _, n := range nodes {
		if path.Base(n.RelPath) == "vault.cryptomator" {
			dir := path.Dir(n.RelPath)
			out = append(out, VaultRef{
				Name:    path.Base(dir),
				RelPath: dir,
			})
		}
	}
	return out, nil
}

// OpenVault downloads the vault's ciphertext subtree from the server, decrypts it with
// the password, and returns the plaintext directory path. The temp dir is retained in
// the session so CloseVault can re-encrypt and upload it.
func (c *Controller) OpenVault(ctx context.Context, vaultRelPath, password string) (string, error) {
	return c.openVaultCore(ctx, vaultRelPath, func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
		return vm.Open(vi, password)
	})
}

// OpenVaultWithRecovery opens the vault using its recovery phrase instead of the
// password (for when the password is lost). The phrase is decoded to the master keys
// and verified against the vault.
func (c *Controller) OpenVaultWithRecovery(ctx context.Context, vaultRelPath, phrase string) (string, error) {
	encKey, macKey, err := vault.RecoveryToKeys(strings.TrimSpace(phrase))
	if err != nil {
		return "", err
	}
	return c.openVaultCore(ctx, vaultRelPath, func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error) {
		return vm.OpenWithKeys(vi, encKey, macKey)
	})
}

// openVaultCore handles the shared open flow — idempotent re-open, ciphertext download,
// session storage — and delegates the actual unlock (by password or recovery keys) to
// the unlock callback. On unlock failure the downloaded temp dir is removed.
func (c *Controller) openVaultCore(ctx context.Context, vaultRelPath string, unlock func(vm *vaultmgr.Manager, vi vaultmgr.VaultInfo) (string, error)) (string, error) {
	// Already open → return the existing plaintext dir (idempotent re-open, e.g. when
	// the user closed the OS file-manager window and wants it back).
	c.mu.Lock()
	if s := c.sessions[vaultRelPath]; s != nil {
		pd := s.plainDir
		c.mu.Unlock()
		return pd, nil
	}
	c.mu.Unlock()

	vm, vi, tmp, err := c.downloadVaultCiphertext(ctx, vaultRelPath)
	if err != nil {
		return "", err
	}

	plainDir, err := unlock(vm, vi)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}

	// Store the open session so CloseVault can re-encrypt and upload.
	c.mu.Lock()
	c.sessions[vaultRelPath] = &vaultSession{vm: vm, vi: vi, tmpDir: tmp, relPath: vaultRelPath, plainDir: plainDir}
	c.mu.Unlock()

	return plainDir, nil
}

// downloadVaultCiphertext refreshes the index and downloads the vault's ciphertext
// subtree into a fresh temp dir, returning a vaultmgr for it. The temp dir is removed
// on error; otherwise the caller owns it (stored in the session).
func (c *Controller) downloadVaultCiphertext(ctx context.Context, vaultRelPath string) (*vaultmgr.Manager, vaultmgr.VaultInfo, string, error) {
	// Sync the index first so we download the CURRENT vault contents (e.g. files added
	// from the web client or just-created), not a stale snapshot.
	if _, err := c.Refresh(ctx); err != nil {
		return nil, vaultmgr.VaultInfo{}, "", err
	}

	tmp, err := os.MkdirTemp("", "ddvopen-")
	if err != nil {
		return nil, vaultmgr.VaultInfo{}, "", err
	}

	nodes, err := c.idx.All()
	if err != nil {
		os.RemoveAll(tmp)
		return nil, vaultmgr.VaultInfo{}, "", err
	}

	prefix := vaultRelPath + "/"
	for _, n := range nodes {
		if n.RelPath != vaultRelPath && !strings.HasPrefix(n.RelPath, prefix) {
			continue
		}
		// Compute path relative to the vault root.
		var rel string
		if n.RelPath == vaultRelPath {
			rel = "."
		} else {
			rel = n.RelPath[len(prefix):]
		}

		// rel derives from the server's RelPath; contain it to the per-open temp dir
		// (which lives OUTSIDE the vault storage) so a malicious server can't traverse
		// out of tmp via ../ in a node path.
		localPath, err := safepath.Join(tmp, rel)
		if err != nil {
			os.RemoveAll(tmp)
			return nil, vaultmgr.VaultInfo{}, "", err
		}
		if n.IsDir {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", err
			}
			f, err := os.Create(localPath)
			if err != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", err
			}
			dlErr := c.srv.Download(ctx, n.NodeID, f)
			closeErr := f.Close()
			if dlErr != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", dlErr
			}
			if closeErr != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", closeErr
			}
		}
	}

	vm, err := vaultmgr.New(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, vaultmgr.VaultInfo{}, "", err
	}
	vi := vaultmgr.VaultInfo{Name: path.Base(vaultRelPath), Dir: tmp}
	return vm, vi, tmp, nil
}

// IsVaultOpen reports whether vaultRelPath has an open (decrypted) session.
func (c *Controller) IsVaultOpen(vaultRelPath string) bool {
	c.mu.Lock()
	_, ok := c.sessions[vaultRelPath]
	c.mu.Unlock()
	return ok
}

// CloseVault re-encrypts the open vault's plaintext back into ciphertext and uploads it
// to the server, then ends the session. Returns an error if the vault is not open or the
// upload fails. On upload failure the session and ciphertext tmpDir are kept so no data
// is lost; note that a second CloseVault call after a failed upload will return ErrLocked
// from vaultmgr because the vault keys are cleared on the first successful re-encrypt.
//
// NOTE: sub-directories in d/ get fresh random UUID names on each EncryptTree call, so
// repeated close+open cycles may leave orphan ciphertext directories on the server
// (cosmetic bloat). Orphan cleanup is out of scope for this version.
func (c *Controller) CloseVault(ctx context.Context, vaultRelPath string) error {
	// Atomically CLAIM the session: remove it from the map under the lock so a concurrent
	// CloseVault can't grab the same *vaultmgr.Manager and race vm.Close on it. On any
	// failure we re-insert the session so it can be retried / closed again.
	c.mu.Lock()
	s := c.sessions[vaultRelPath]
	if s != nil {
		delete(c.sessions, vaultRelPath)
	}
	c.mu.Unlock()

	if s == nil {
		return fmt.Errorf("vault %q is not open", vaultRelPath)
	}

	// Re-encrypt plaintext → ciphertext in s.tmpDir, wipe plaintext.
	if err := s.vm.Close(s.vi); err != nil {
		c.mu.Lock()
		c.sessions[vaultRelPath] = s
		c.mu.Unlock()
		return err
	}

	// Upload the updated ciphertext tree (masterkey/vault.cryptomator stay identical;
	// d/ gets fresh content from EncryptTree). On error keep the session+tmpDir so the
	// ciphertext is not lost.
	if err := c.uploadTree(ctx, s.tmpDir, s.relPath); err != nil {
		c.mu.Lock()
		c.sessions[vaultRelPath] = s
		c.mu.Unlock()
		return err
	}

	// Success: the session was already removed from the map; drop the ciphertext tmpDir.
	_ = os.RemoveAll(s.tmpDir)
	return nil
}
