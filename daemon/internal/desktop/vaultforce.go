package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"discodrive.org/daemon/internal/localname"
	"discodrive.org/daemon/internal/vaultmgr"
)

// vaultPlainRootFor is where the desktop app decrypts open vaults for one pairing: its own
// folder per server and profile, inside the user cache folder and outside every synced
// folder. Vault plaintext folders are named after the vault, so in a folder shared with
// another server (or with the tray, which uses vaultmgr's default) a crash leftover could
// be taken for a vault of the same name and restored into it; here it cannot, and a
// sign-out can sweep everything the desktop left without touching anyone else's.
func vaultPlainRootFor(serverURL, profileDir string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(serverURL + "\n" + profileDir))
	return filepath.Join(cache, "discodrive", "desktop-open", hex.EncodeToString(sum[:8]))
}

// tempDir is where the ciphertext temp folders go.
func (c *Controller) tempDir() string {
	if c.tempRoot != "" {
		return c.tempRoot
	}
	return os.TempDir()
}

// ErrVaultLeftovers: decrypted vault files an earlier session left behind (a crash, or a
// close that never finished) are still on disk. They may hold changes that were never
// saved, so they are not deleted; see SweepVaultLeftovers.
var ErrVaultLeftovers = errors.New("decrypted vault files from an earlier session are still on this computer and may hold unsaved changes")

// notEncrypted labels a recovery folder that holds decrypted files.
const notEncrypted = " (NOT ENCRYPTED)"

// ForceCloseAllVaults ends every open vault session without the server, for a sign-out the
// user confirmed although the vaults cannot be saved (the server is gone, the device was
// revoked). Unsaved changes are never dropped, and it is all or nothing as far as it can be:
//
//  1. Every vault is kept first, in recoveryRoot. With the vault's keys in memory, the
//     session is encrypted again, locally, into a complete vault folder
//     ("<vault> <time>"), which the vault's password opens like the original; nothing is
//     kept when nothing changed. Only when the keys (or the opening snapshot) are missing
//     are the decrypted files copied instead, as "<vault> <time> (NOT ENCRYPTED)". Any
//     other failure — an unreadable file, a full disk — stops here: every copy made so far
//     is removed and no session is closed, so nothing ever leaves decrypted because
//     encrypting it failed once.
//  2. Then the sessions are closed and their plaintext removed. A vault whose close fails
//     (its files changed in between) stays open, and its copy is removed while its
//     plaintext is still whole. The vaults that did close are returned with the error.
//
// It returns the folders it created for the vaults it closed.
func (c *Controller) ForceCloseAllVaults(recoveryRoot string) ([]string, error) {
	type plan struct {
		rel        string
		s          *vaultSession
		vi         vaultmgr.VaultInfo
		p          *vaultmgr.PreparedClose // nil when only the cleanup is left, or no keys
		kept       string                  // its copy in recoveryRoot; "" when nothing changed
		decrypted  bool                    // kept holds decrypted files (no keys)
		finishOnly bool                    // saved remotely or in recovery; only cleanup remains
	}
	c.mu.Lock()
	for _, s := range c.sessions {
		if s.closing {
			c.mu.Unlock()
			return nil, fmt.Errorf("vault is already being saved")
		}
	}
	plans := make([]*plan, 0, len(c.sessions))
	for rel, s := range c.sessions {
		s.closing = true
		plans = append(plans, &plan{rel: rel, s: s, vi: s.vi, kept: s.recoveryCopy, finishOnly: s.finishing != nil})
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		for _, pl := range plans {
			pl.s.closing = false
		}
		c.mu.Unlock()
	}()
	dropStage := func(pl *plan) {
		if pl.p != nil && pl.p.Dir != pl.vi.Dir {
			os.RemoveAll(pl.p.Dir)
		}
	}

	// 1. Keep every vault.
	for _, pl := range plans {
		if pl.finishOnly {
			continue
		}
		p, err := pl.s.vm.PrepareClose(pl.vi)
		switch {
		case err == nil:
			pl.p = p
			if p.Dir != pl.vi.Dir {
				pl.kept, err = copyToRecovery(p.Dir, recoveryRoot, pl.vi.Name, "")
			}
		case errors.Is(err, vaultmgr.ErrLocked) || errors.Is(err, vaultmgr.ErrNoSnapshot):
			// Nothing to encrypt with: keep the decrypted files themselves.
			pl.decrypted = true
			pl.kept, err = copyToRecovery(pl.s.plainDir, recoveryRoot, pl.vi.Name, notEncrypted)
		}
		if err != nil {
			for _, q := range plans {
				if q.kept != "" && !q.finishOnly {
					os.RemoveAll(q.kept)
				}
				dropStage(q)
			}
			return nil, fmt.Errorf("vault %s: %w", pl.vi.Name, err)
		}
	}
	if afterKeepingAll != nil {
		afterKeepingAll()
	}

	// 2. Close them.
	var kept []string
	var errs []error
	for _, pl := range plans {
		var err error
		intact := true // after a failure: the plaintext is still whole
		switch {
		case pl.finishOnly:
			intact = false // the saved recovery must survive another cleanup failure
			err = c.finishVaultClose(pl.rel, pl.s)
		case pl.decrypted:
			// The decrypted folder itself replaces the copy: an edit made since the copy
			// is not lost.
			var touched bool
			pl.kept, touched, err = settleDecrypted(pl.s.plainDir, pl.kept, recoveryRoot, pl.vi.Name)
			intact = !touched
		default:
			err = pl.s.vm.FinishClose(pl.vi, pl.p)
			intact = !pl.p.CleanupStarted()
			if err != nil && !intact {
				// Plaintext has already been detached. A retry must finish that
				// cleanup, never prepare another snapshot from the missing path.
				c.mu.Lock()
				pl.s.finishing, pl.s.recoveryCopy = pl.p, pl.kept
				c.mu.Unlock()
			}
		}
		dropStage(pl)
		if err != nil {
			if pl.kept != "" {
				if intact {
					os.RemoveAll(pl.kept) // the open session still has it all
				} else {
					kept = append(kept, pl.kept)
				}
			}
			errs = append(errs, fmt.Errorf("vault %s: %w", pl.vi.Name, err))
			continue
		}
		if !pl.finishOnly {
			c.mu.Lock()
			delete(c.sessions, pl.rel)
			tmp, pending := pl.s.tmpDir, pl.s.pending
			c.mu.Unlock()
			if pending != nil && pending.Dir != tmp {
				os.RemoveAll(pending.Dir)
			}
			os.RemoveAll(tmp)
		}
		if pl.kept != "" {
			kept = append(kept, pl.kept)
		}
	}
	return kept, errors.Join(errs...)
}

// afterKeepingAll runs between keeping every vault and closing them; tests use it.
var afterKeepingAll func()

// SweepLeftoversFor is SweepVaultLeftovers for a pairing whose account could not be opened:
// the plaintext folder of serverURL and profileDir, and the ciphertext temp folders in
// tempRoot ("" is os.TempDir()).
func SweepLeftoversFor(serverURL, profileDir, recoveryRoot, tempRoot string, force bool) ([]string, error) {
	c := &Controller{vaultPlainRoot: vaultPlainRootFor(serverURL, profileDir), tempRoot: tempRoot}
	return c.SweepVaultLeftovers(recoveryRoot, force)
}

// SweepVaultLeftovers removes what vault sessions of this pairing left on disk, once no
// vault is open: the desktop's plaintext folder (see vaultPlainRootFor). Temporary
// ciphertext belongs to individual sessions and is not swept. Plaintext already saved
// (.closing-*) just goes. Any other decrypted leftover may hold unsaved changes: unless
// force is set, nothing is removed and ErrVaultLeftovers is returned; with force, each is
// moved to recoveryRoot as "<name> <time> (NOT ENCRYPTED)" (there are no keys to encrypt
// it with). It returns the folders it created.
func (c *Controller) SweepVaultLeftovers(recoveryRoot string, force bool) ([]string, error) {
	c.mu.Lock()
	open := len(c.sessions)
	c.mu.Unlock()
	if open > 0 {
		return nil, fmt.Errorf("%d vaults are still open", open)
	}
	var kept []string
	if root := c.vaultPlainRoot; root != "" {
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		var unsaved []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".closing-") {
				os.RemoveAll(filepath.Join(root, e.Name()))
				continue
			}
			unsaved = append(unsaved, e.Name())
		}
		if len(unsaved) > 0 && !force {
			return nil, ErrVaultLeftovers
		}
		for _, name := range unsaved {
			p, err := moveToRecovery(filepath.Join(root, name), recoveryRoot, strings.TrimLeft(name, "."), notEncrypted)
			if err != nil {
				return kept, err
			}
			kept = append(kept, p)
		}
		if err := os.RemoveAll(root); err != nil {
			return kept, err
		}
	}
	// Session close removes the exact temporary directories it owns. Never sweep
	// os.TempDir by prefix: another process may still be using those snapshots.
	if err := checkLegacyVaultLeftovers(); err != nil {
		return kept, err
	}
	return kept, nil
}

// recoveryPath is a new folder name in root for name, stamped with the time.
// The folder is made private to the user even if it was there before (a failure to is
// only logged: it may belong to someone else); name becomes a name the local file system
// accepts (a server vault name can hold characters Windows rejects).
func recoveryPath(root, name, suffix string) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		log.Printf("desktop: cannot make %s private: %v", root, err)
	}
	name = localname.Localize(name)
	if name == "" || name == "." || name == ".." {
		name = "vault"
	}
	base := name + " " + time.Now().Format("2006-01-02 15.04.05")
	for i := 1; ; i++ {
		p := filepath.Join(root, base+suffix)
		if i > 1 {
			p = filepath.Join(root, fmt.Sprintf("%s %d%s", base, i, suffix))
		}
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p, nil
		} else if err != nil {
			return "", err
		}
	}
}

// moveToRecovery moves src into a new folder in root; across file systems it copies and
// then removes src. A decrypted folder is excluded from backups before it moves.
func moveToRecovery(src, root, name, suffix string) (string, error) {
	dst, err := recoveryPath(root, name, suffix)
	if err != nil {
		return "", err
	}
	if suffix == notEncrypted {
		excludeFromBackup(src)
	}
	if err := renameDir(src, dst); err != nil {
		if err := copyIntoNew(src, dst, suffix); err != nil {
			os.RemoveAll(dst)
			return "", err
		}
		if err := os.RemoveAll(src); err != nil {
			return dst, err
		}
	}
	return dst, nil
}

// copyToRecovery copies src into a new folder in root.
func copyToRecovery(src, root, name, suffix string) (string, error) {
	dst, err := recoveryPath(root, name, suffix)
	if err != nil {
		return "", err
	}
	if err := copyIntoNew(src, dst, suffix); err != nil {
		os.RemoveAll(dst)
		return "", err
	}
	return dst, nil
}

// copyIntoNew creates dst and copies src into it. A decrypted copy's folder is excluded
// from backups while it is still empty, so no decrypted file is ever in a folder a backup
// may pick up.
func copyIntoNew(src, dst, suffix string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if suffix == notEncrypted {
		excludeFromBackup(dst)
	}
	return copyTree(src, dst)
}

// settleDecrypted ends a decrypted vault whose files were copied to kept: the folder
// itself takes the copy's place, so whatever was written since the copy is kept too. On
// one volume that is a rename; across volumes the copy is brought up to date and checked
// against the folder before the folder is removed. It returns where the files are now, and
// whether plain was touched (false: plain is whole, and kept may be dropped).
func settleDecrypted(plain, kept, root, name string) (string, bool, error) {
	excludeFromBackup(plain)
	dst, err := recoveryPath(root, name, notEncrypted)
	if err != nil {
		return kept, false, err
	}
	if err := renameDir(plain, dst); err == nil {
		// Take the copy's name, so the folder the user is told about is the one listed.
		if os.RemoveAll(kept) == nil && os.Rename(dst, kept) == nil {
			return kept, true, nil
		}
		return dst, true, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		same, err := sameTree(plain, kept)
		if err != nil {
			return kept, false, err
		}
		if same {
			if err := os.RemoveAll(plain); err != nil {
				return kept, true, err
			}
			return kept, true, nil
		}
		if err := os.RemoveAll(kept); err != nil {
			return kept, false, err
		}
		if err := copyIntoNew(plain, kept, notEncrypted); err != nil {
			return kept, false, err
		}
	}
	return kept, false, fmt.Errorf("the decrypted files kept changing while they were being kept")
}

// renameDir is os.Rename; tests make it fail as across volumes.
var renameDir = os.Rename

// sameTree reports whether a and b hold the same folders and files with the same content.
func sameTree(a, b string) (bool, error) {
	list := func(root string) (map[string]string, error) {
		out := map[string]string{}
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			switch {
			case info.IsDir():
				out[rel] = "dir"
			case info.Mode().IsRegular():
				f, err := os.Open(p)
				if err != nil {
					return err
				}
				defer f.Close()
				h := sha256.New()
				if _, err := io.Copy(h, f); err != nil {
					return err
				}
				out[rel] = hex.EncodeToString(h.Sum(nil))
			}
			return nil
		})
		return out, err
	}
	la, err := list(a)
	if err != nil {
		return false, err
	}
	lb, err := list(b)
	if err != nil {
		return false, err
	}
	if len(la) != len(lb) {
		return false, nil
	}
	for k, v := range la {
		if lb[k] != v {
			return false, nil
		}
	}
	return true, nil
}

// excludeFromBackup marks a folder to be left out of backups where that is cheap and safe:
// on macOS, Time Machine's sticky exclusion (tmutil addexclusion, no admin rights needed).
// Best effort; tests replace it.
var excludeFromBackup = func(p string) {
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "tmutil", "addexclusion", p).CombinedOutput(); err != nil {
		log.Printf("desktop: cannot exclude %s from Time Machine: %v %s", p, err, out)
	}
}

// copyTree copies the folders and regular files of src to dst, private to the user.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o700)
		case info.Mode().IsRegular():
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
		return nil
	})
}
