package desktop

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/safepath"
	"discodrive.org/daemon/internal/vault"
	"discodrive.org/daemon/internal/vaultmgr"
)

// vaultSession records the open state of a decrypted vault so CloseVault can
// re-encrypt the plaintext and upload it back to the server.
type vaultSession struct {
	vm           *vaultmgr.Manager
	vi           vaultmgr.VaultInfo
	tmpDir       string // local ciphertext dir (downloaded from server; re-encrypted into on Close)
	relPath      string // server-relative vault folder
	plainDir     string // decrypted plaintext dir (for idempotent re-open)
	remote       map[string]index.Node
	pending      *vaultmgr.PreparedClose
	finishing    *vaultmgr.PreparedClose
	recoveryCopy string // locally saved recovery when forced close is awaiting cleanup
	closing      bool
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
	tmp, err := os.MkdirTemp(c.tempDir(), "ddvault-")
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
	defer c.trimVaultCache()
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
		if err == nil {
			c.cacheVaultCiphertext(p)
		}
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
		if s.closing || (s.finishing != nil && s.finishing.CleanupStarted()) {
			c.mu.Unlock()
			return "", fmt.Errorf("vault is being saved")
		}
		c.mu.Unlock()
		return pd, nil
	}
	// One open at a time per vault: a second one would find the first one's plaintext
	// and take it for leftovers of a crash.
	if c.opening[vaultRelPath] {
		c.mu.Unlock()
		return "", fmt.Errorf("vault is being opened")
	}
	c.opening[vaultRelPath] = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.opening, vaultRelPath); c.mu.Unlock() }()

	vm, vi, tmp, remote, err := c.downloadVaultSnapshot(ctx, vaultRelPath)
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
	c.sessions[vaultRelPath] = &vaultSession{vm: vm, vi: vi, tmpDir: tmp, relPath: vaultRelPath, plainDir: plainDir, remote: remote}
	c.mu.Unlock()

	return plainDir, nil
}

// downloadVaultCiphertext refreshes the index and downloads the vault's ciphertext
// subtree into a fresh temp dir, returning a vaultmgr for it. The temp dir is removed
// on error; otherwise the caller owns it (stored in the session).
func (c *Controller) downloadVaultCiphertext(ctx context.Context, vaultRelPath string) (*vaultmgr.Manager, vaultmgr.VaultInfo, string, error) {
	vm, vi, dir, _, err := c.downloadVaultSnapshot(ctx, vaultRelPath)
	return vm, vi, dir, err
}

// Capture the same index snapshot used for downloads. Reading the index again
// after decryption could adopt a newer version for older local bytes.
func (c *Controller) downloadVaultSnapshot(ctx context.Context, vaultRelPath string) (*vaultmgr.Manager, vaultmgr.VaultInfo, string, map[string]index.Node, error) {
	defer c.trimVaultCache()
	// Sync the index first so we download the CURRENT vault contents (e.g. files added
	// from the web client or just-created), not a stale snapshot.
	if _, err := c.Refresh(ctx); err != nil {
		return nil, vaultmgr.VaultInfo{}, "", nil, err
	}

	tmp, err := os.MkdirTemp(c.tempDir(), "ddvopen-")
	if err != nil {
		return nil, vaultmgr.VaultInfo{}, "", nil, err
	}

	nodes, err := c.idx.All()
	if err != nil {
		os.RemoveAll(tmp)
		return nil, vaultmgr.VaultInfo{}, "", nil, err
	}

	type download struct{ nodeID, localPath, hash string }
	var downloads []download
	prefix := vaultRelPath + "/"
	remote := map[string]index.Node{}
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
			remote[rel] = n
		}

		// rel derives from the server's RelPath; contain it to the per-open temp dir
		// (which lives OUTSIDE the vault storage) so a malicious server can't traverse
		// out of tmp via ../ in a node path.
		localPath, err := safepath.Join(tmp, rel)
		if err != nil {
			os.RemoveAll(tmp)
			return nil, vaultmgr.VaultInfo{}, "", nil, err
		}
		if n.IsDir {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", nil, err
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
				os.RemoveAll(tmp)
				return nil, vaultmgr.VaultInfo{}, "", nil, err
			}
			downloads = append(downloads, download{n.NodeID, localPath, n.ContentHash})
		}
	}

	// Bound both network requests and open files. Wait for every worker before
	// removing the temporary directory, including on cancellation or an error.
	downloadCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan download)
	var workers sync.WaitGroup
	var failed sync.Once
	var downloadErr error
	for i := 0; i < min(6, len(downloads)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if downloadCtx.Err() != nil {
					continue
				}
				err := func() error {
					if c.restoreVaultCiphertext(job.hash, job.localPath) {
						return nil
					}
					f, err := os.Create(job.localPath)
					if err != nil {
						return err
					}
					err = c.srv.Download(downloadCtx, job.nodeID, f)
					closeErr := f.Close()
					if err != nil {
						return err
					}
					if closeErr != nil {
						return closeErr
					}
					if validVaultHash(job.hash) {
						actual, err := fileSHA256(job.localPath)
						if err != nil {
							return err
						}
						if !strings.EqualFold(actual, job.hash) {
							return fmt.Errorf("vault: ciphertext changed during download; reopen the vault")
						}
					}
					c.cacheVaultCiphertext(job.localPath)
					return nil
				}()
				if err != nil {
					failed.Do(func() { downloadErr = err; cancel() })
				}
			}
		}()
	}
dispatch:
	for _, job := range downloads {
		select {
		case <-downloadCtx.Done():
			break dispatch
		case jobs <- job:
		}
	}
	close(jobs)
	workers.Wait()
	if downloadErr == nil {
		downloadErr = ctx.Err()
	}
	if downloadErr != nil {
		os.RemoveAll(tmp)
		return nil, vaultmgr.VaultInfo{}, "", nil, downloadErr
	}

	vm, err := vaultmgr.New(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, vaultmgr.VaultInfo{}, "", nil, err
	}
	if c.vaultPlainRoot != "" {
		vm.CacheRoot = c.vaultPlainRoot
	}
	// The ciphertext dir is a fresh temp copy each time; the plaintext folder is keyed
	// by the profile and the vault's server path instead.
	vi := vaultmgr.VaultInfo{Name: path.Base(vaultRelPath), Dir: tmp, ID: c.contentDir + "\n" + vaultRelPath}
	return vm, vi, tmp, remote, nil
}

// IsVaultOpen reports whether vaultRelPath has an open (decrypted) session.
func (c *Controller) IsVaultOpen(vaultRelPath string) bool {
	c.mu.Lock()
	_, ok := c.sessions[vaultRelPath]
	c.mu.Unlock()
	return ok
}

// CloseVault commits only changed ciphertext, retaining plaintext and keys on
// failure. A pending staged tree is reused on retry, including uncertain uploads.
func (c *Controller) CloseVault(ctx context.Context, vaultRelPath string) error {
	c.mu.Lock()
	s := c.sessions[vaultRelPath]
	if s == nil {
		c.mu.Unlock()
		return fmt.Errorf("vault %q is not open", vaultRelPath)
	}
	if s.closing {
		c.mu.Unlock()
		return fmt.Errorf("vault is already being saved")
	}
	s.closing = true
	finishing, pending := s.finishing, s.pending
	c.mu.Unlock()
	defer func() { c.mu.Lock(); s.closing = false; c.mu.Unlock() }()
	// s.closing makes this call the session's only writer, but other calls (a re-open,
	// IsVaultOpen) read it under c.mu from their own goroutines, so every field change
	// below is made under c.mu too.
	if finishing != nil {
		return c.finishVaultClose(vaultRelPath, s)
	}
	if pending == nil {
		p, err := s.vm.PrepareClose(c.sessionInfo(s))
		if err != nil {
			return err
		}
		c.mu.Lock()
		s.pending = p
		c.mu.Unlock()
	}
	if err := c.commitVaultDelta(ctx, s); err != nil {
		return err
	}
	c.mu.Lock()
	old := s.tmpDir
	p := s.pending
	s.tmpDir = p.Dir
	s.vi.Dir = p.Dir
	vi := s.vi
	s.pending = nil
	c.mu.Unlock()
	s.vm.AcceptClose(vi, p)
	if old != p.Dir {
		_ = os.RemoveAll(old)
	}
	c.mu.Lock()
	s.finishing = p
	c.mu.Unlock()
	return c.finishVaultClose(vaultRelPath, s)
}

// sessionInfo reads the session's vault info under c.mu.
func (c *Controller) sessionInfo(s *vaultSession) vaultmgr.VaultInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return s.vi
}

func (c *Controller) finishVaultClose(vaultRelPath string, s *vaultSession) error {
	c.mu.Lock()
	vi, finishing := s.vi, s.finishing
	c.mu.Unlock()
	if err := s.vm.FinishClose(vi, finishing); err != nil {
		if !finishing.CleanupStarted() {
			c.mu.Lock()
			s.finishing = nil
			c.mu.Unlock()
		}
		return err
	}
	c.mu.Lock()
	delete(c.sessions, vaultRelPath)
	tmp, pending := s.tmpDir, s.pending
	c.mu.Unlock()
	if pending != nil && pending.Dir != tmp {
		_ = os.RemoveAll(pending.Dir)
	}
	_ = os.RemoveAll(tmp)
	return nil
}
