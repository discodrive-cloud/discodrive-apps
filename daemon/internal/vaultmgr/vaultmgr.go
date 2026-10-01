package vaultmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"discodrive.org/daemon/internal/vault"
)

const markerFile = "vault.cryptomator"

// ErrLocked is returned when vault keys are not in memory (Open must be called first).
var ErrLocked = errors.New("vault is not unlocked in memory (open it again)")

// ErrVaultBusy is returned when the vault is already being opened or closed by another
// call (a double click in the tray menu runs two handlers at once).
var ErrVaultBusy = errors.New("vault is busy: it is already being opened or closed")

// VaultInfo describes a detected vault.
type VaultInfo struct {
	Name string // vault folder name
	Dir  string // absolute path of the vault folder (inside SyncDir)
	// ID is a stable identity for the plaintext folder when Dir is not stable
	// (the desktop app decrypts from a fresh temp copy each time). Empty means Dir.
	ID string
}

// Manager manages vaults in SyncDir; plaintext of open vaults is kept in CacheRoot (OUTSIDE SyncDir).
type Manager struct {
	SyncDir   string
	CacheRoot string // e.g. <UserCacheDir>/discodrive/open — MUST be outside SyncDir

	mu              sync.Mutex
	snapshots       map[string]*vault.TreeSnapshot
	pendingCleanup  map[string]*PreparedClose   // retained until plaintext cleanup succeeds
	removePlaintext func(string) error          // nil uses os.RemoveAll; injectable for cleanup fault tests
	unlocked        map[string]*vault.Vault     // vault name → keys (while open)
	liveAtOpen      map[string]map[string]stamp // vault name → its d/ files when opened
	busy            map[string]bool             // vault name → an Open or Close is running
}

// acquire marks the vault busy for one Open or Close, or reports ErrVaultBusy when
// another one is already running. The returned func releases it.
func (m *Manager) acquire(name string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy[name] {
		return nil, ErrVaultBusy
	}
	if m.busy == nil {
		m.busy = map[string]bool{}
	}
	m.busy[name] = true
	return func() {
		m.mu.Lock()
		delete(m.busy, name)
		m.mu.Unlock()
	}, nil
}

// stamp is what Close compares to tell whether the syncer changed a ciphertext file
// while the vault was open.
type stamp struct {
	size int64
	mod  time.Time
}

// recordLive lists the vault's ciphertext files (relative to d/).
func recordLive(vaultDir string) (map[string]stamp, error) {
	root := filepath.Join(vaultDir, "d")
	out := map[string]stamp{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if os.IsNotExist(err) && p == root {
			return filepath.SkipDir
		}
		if err != nil || fi.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[rel] = stamp{fi.Size(), fi.ModTime()}
		return nil
	})
	return out, err
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
// The folder keeps the vault's name for Finder but carries a short hash of its
// identity: two vaults with the same folder name (A/Secrets and B/Secrets, or the
// tray's and the desktop app's) must never share plaintext.
func (m *Manager) plainDir(vi VaultInfo) string {
	id := vi.ID
	if id == "" {
		id = vi.Dir
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(m.CacheRoot, vi.Name+"-"+hex.EncodeToString(sum[:4]))
}

// IsOpen reports whether the vault is open in this process: its plaintext folder
// exists and its keys are in memory, or committed plaintext still needs cleanup.
// Plaintext left by a crash does not count — the next Open recovers it.
func (m *Manager) IsOpen(vi VaultInfo) bool {
	m.mu.Lock()
	_, ok := m.unlocked[vi.Name]
	pending := m.pendingCleanup[vi.Name]
	m.mu.Unlock()
	if !ok {
		return false
	}
	if pending != nil {
		return true
	}
	_, err := os.Stat(m.plainDir(vi))
	return err == nil
}

// Create creates a new vault named name in SyncDir. name is a single folder name: a path
// ("../x", "a/b") would put the vault outside SyncDir or somewhere the tray never looks.
func (m *Manager) Create(name, password string) (VaultInfo, error) {
	if err := vault.CheckPlainName(name); err != nil {
		return VaultInfo{}, err
	}
	if strings.Contains(name, `\`) {
		return VaultInfo{}, fmt.Errorf("%w: %q", vault.ErrInvalidName, name)
	}
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
	release, err := m.acquire(vi.Name)
	if err != nil {
		return "", err
	}
	defer release()
	v, err := vault.Open(vi.Dir, password)
	if err != nil {
		return "", err
	}
	return m.openUnlocked(vi, v)
}

// OpenWithKeys opens a vault using master keys recovered from a recovery phrase,
// bypassing the password.
func (m *Manager) OpenWithKeys(vi VaultInfo, encKey, macKey []byte) (string, error) {
	release, err := m.acquire(vi.Name)
	if err != nil {
		return "", err
	}
	defer release()
	v, err := vault.OpenWithKeys(vi.Dir, encKey, macKey)
	if err != nil {
		return "", err
	}
	return m.openUnlocked(vi, v)
}

// RecoveredPrefix names the folder, inside a reopened vault, that holds plaintext
// edits a crash or a failed close left behind. Closing the vault encrypts it like
// any other edit, so nothing unsaved is lost and no plaintext stays outside.
const RecoveredPrefix = "Recovered unsaved changes "

// openUnlocked decrypts the (already-unlocked) vault into plainDir, snapshots it for
// an incremental close, and stores its keys. Plaintext left from an earlier session
// is never decrypted over: it is set aside and its changed files come back inside the
// vault under RecoveredPrefix. On any failure nothing new stays on disk.
func (m *Manager) openUnlocked(vi VaultInfo, v *vault.Vault) (_ string, err error) {
	m.mu.Lock()
	pending := m.pendingCleanup[vi.Name]
	m.mu.Unlock()
	if pending != nil {
		return "", fmt.Errorf("vault cleanup is pending; close the vault again")
	}
	pd := m.plainDir(vi)
	if err := os.MkdirAll(m.CacheRoot, 0o700); err != nil {
		return "", err
	}
	m.removeStaleCleanups()
	// Leftovers: this vault's folder, or one a crash left under the name used before
	// plaintext folders carried an identity hash.
	leftover := ""
	if _, statErr := os.Lstat(pd); statErr == nil {
		leftover = pd
	} else if legacy := filepath.Join(m.CacheRoot, vi.Name); legacy != pd {
		if fi, lerr := os.Lstat(legacy); lerr == nil && fi.IsDir() {
			leftover = legacy
		}
	}
	stash := ""
	if leftover != "" {
		stash, err = os.MkdirTemp(m.CacheRoot, ".unsaved-")
		if err != nil {
			return "", err
		}
		if err := os.Rename(leftover, filepath.Join(stash, "tree")); err != nil {
			os.Remove(stash)
			return "", err
		}
	}
	defer func() {
		if err == nil {
			return
		}
		os.RemoveAll(pd)
		if stash != "" {
			if os.Rename(filepath.Join(stash, "tree"), leftover) == nil {
				os.Remove(stash)
			}
		}
	}()
	if err := os.Mkdir(pd, 0o700); err != nil {
		return "", err
	}
	// Empty vault (no d/) — nothing to decrypt.
	if _, statErr := os.Stat(filepath.Join(vi.Dir, "d")); statErr == nil {
		if err := v.DecryptTree(vi.Dir, pd); err != nil {
			return "", err
		}
	}
	snap, err := v.SnapshotTree(pd, vi.Dir)
	if err != nil {
		return "", err
	}
	live, err := recordLive(vi.Dir)
	if err != nil {
		return "", err
	}
	if stash != "" {
		if err := restoreUnsaved(filepath.Join(stash, "tree"), pd); err != nil {
			return "", err
		}
		os.RemoveAll(stash)
	}
	m.storeUnlocked(vi.Name, v)
	m.mu.Lock()
	if m.snapshots == nil {
		m.snapshots = map[string]*vault.TreeSnapshot{}
	}
	m.snapshots[vi.Name] = snap
	if m.liveAtOpen == nil {
		m.liveAtOpen = map[string]map[string]stamp{}
	}
	m.liveAtOpen[vi.Name] = live
	m.mu.Unlock()
	return pd, nil
}

// removeStaleCleanups deletes .closing-* folders no close in this process is still
// retrying. FinishClose moves plaintext there only after the vault matched it, so
// what is left — after a failed cleanup and a quit or crash — is an already-saved
// decrypted copy that would otherwise stay on disk, hidden, for good. Best effort:
// one that cannot be removed now is tried again on the next open.
func (m *Manager) removeStaleCleanups() {
	m.mu.Lock()
	retrying := map[string]bool{}
	for _, p := range m.pendingCleanup {
		retrying[p.cleanupPath] = true
	}
	m.mu.Unlock()
	stale, _ := filepath.Glob(filepath.Join(m.CacheRoot, ".closing-*"))
	for _, dir := range stale {
		if !retrying[dir] {
			os.RemoveAll(dir)
		}
	}
}

// restoreUnsaved copies every file of old that is missing from, or differs in, the
// freshly decrypted fresh tree into fresh/<RecoveredPrefix><time>/. It copies rather
// than moves: if it fails part-way, old is still whole when the open is rolled back.
func restoreUnsaved(old, fresh string) error {
	dest := filepath.Join(fresh, RecoveredPrefix+time.Now().Format("2006-01-02 15.04.05"))
	return filepath.Walk(old, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !info.Mode().IsRegular() || info.Name() == ".DS_Store" {
			return err
		}
		rel, err := filepath.Rel(old, p)
		if err != nil {
			return err
		}
		if same, _ := sameContent(p, filepath.Join(fresh, rel)); same {
			return nil
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func sameContent(a, b string) (bool, error) {
	ha, err := fileHash(a)
	if err != nil {
		return false, err
	}
	hb, err := fileHash(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func fileHash(p string) ([sha256.Size]byte, error) {
	var out [sha256.Size]byte
	f, err := os.Open(p)
	if err != nil {
		return out, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return out, err
	}
	copy(out[:], h.Sum(nil))
	return out, nil
}

// Close saves the open vault in place, for vaults that live inside a synced folder
// (the tray). The new ciphertext tree is staged OUTSIDE SyncDir, then only the entries
// that changed are written into the vault and only the ones that disappeared are
// removed — the live syncer never sees an emptied d/ or a tree with fresh ids, and a
// failure part-way leaves only valid, possibly duplicated, entries. Plaintext and keys
// are dropped only after the vault matches the stage.
// If keys are not in memory (failed Open, crash, new process) — returns ErrLocked; plaintext is untouched.
func (m *Manager) Close(vi VaultInfo) error {
	release, err := m.acquire(vi.Name)
	if err != nil {
		return err
	}
	defer release()
	m.mu.Lock()
	v, snap, live := m.unlocked[vi.Name], m.snapshots[vi.Name], m.liveAtOpen[vi.Name]
	pending := m.pendingCleanup[vi.Name]
	// applyStage advances the stamps of what it writes; it works on a copy, stored back
	// under the lock, so no one else ever sees the map while it changes.
	var atOpen map[string]stamp
	if live != nil {
		atOpen = make(map[string]stamp, len(live))
		for k, st := range live {
			atOpen[k] = st
		}
	}
	m.mu.Unlock()
	if pending != nil {
		return m.FinishClose(vi, pending)
	}
	if v == nil {
		return ErrLocked
	}
	if snap == nil || atOpen == nil {
		return fmt.Errorf("vault %q: missing opening snapshot", vi.Name)
	}
	pd := m.plainDir(vi)
	if _, err := os.Stat(pd); err != nil {
		return fmt.Errorf("vault %q is not open", vi.Name)
	}
	stage, after, err := v.PrepareTreeIn(pd, vi.Dir, snap, m.CacheRoot)
	if err != nil {
		return err
	}
	if stage != vi.Dir {
		defer os.RemoveAll(stage)
		err := applyStage(stage, vi.Dir, atOpen)
		m.mu.Lock()
		// Kept even on failure: a retry must recognise the entries this attempt wrote.
		m.liveAtOpen[vi.Name] = atOpen
		if err == nil {
			m.snapshots[vi.Name] = after
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return m.FinishClose(vi, &PreparedClose{Dir: vi.Dir, snapshot: after})
}

// ErrChangedElsewhere: a ciphertext entry this close would rewrite was changed by the
// syncer (another device) while the vault was open. Nothing is written; the plaintext
// stays open so the user can keep their version aside and reopen.
var ErrChangedElsewhere = errors.New("the vault was changed on another device while it was open; copy your edits out, then close and reopen it")

// applyStage brings the vault's d/ tree to the stage, touching only what this session
// changed. The stage was cloned from the live tree when the close started, and the
// syncer keeps working meanwhile, so against atOpen (the tree when the vault was
// opened): an entry to rewrite must be unchanged since the open, and an entry to
// remove must be one this session saw — what another device added stays.
func applyStage(stage, vaultDir string, atOpen map[string]stamp) error {
	stageD, liveD := filepath.Join(stage, "d"), filepath.Join(vaultDir, "d")
	unchanged := func(rel string, fi os.FileInfo) bool {
		st, ok := atOpen[rel]
		return ok && st.size == fi.Size() && st.mod.Equal(fi.ModTime())
	}
	type op struct {
		rel     string
		isDir   bool
		replace bool // an existing entry of the other type goes first
	}
	var ops []op
	// Plan and check everything before the first write.
	err := filepath.Walk(stageD, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(stageD, p)
		if err != nil || rel == "." {
			return err
		}
		live, lerr := os.Lstat(filepath.Join(liveD, rel))
		switch {
		// ENOTDIR: a parent is a file this plan already replaces with a folder.
		case os.IsNotExist(lerr) || errors.Is(lerr, syscall.ENOTDIR):
			ops = append(ops, op{rel: rel, isDir: info.IsDir()})
		case lerr != nil:
			return lerr
		case info.IsDir() && live.IsDir():
		case info.IsDir(): // a file becomes a folder
			if !unchanged(rel, live) {
				return ErrChangedElsewhere
			}
			ops = append(ops, op{rel: rel, isDir: true, replace: true})
		case live.IsDir(): // a folder becomes a file: its files must all be ours to drop
			werr := filepath.Walk(filepath.Join(liveD, rel), func(q string, fi os.FileInfo, err error) error {
				if err != nil || fi.IsDir() {
					return err
				}
				r, _ := filepath.Rel(liveD, q)
				if !unchanged(r, fi) {
					return ErrChangedElsewhere
				}
				return nil
			})
			if werr != nil {
				return werr
			}
			ops = append(ops, op{rel: rel, replace: true})
		default:
			if same, err := vault.EqualCipherFile(p, filepath.Join(liveD, rel)); err == nil && same {
				return nil
			}
			// Staged as it was at open (the clone hard-links untouched entries): this
			// session did not change it, so whatever is live now stays.
			if st, ok := atOpen[rel]; ok && st.size == info.Size() && st.mod.Equal(info.ModTime()) {
				return nil
			}
			if !unchanged(rel, live) {
				return ErrChangedElsewhere
			}
			ops = append(ops, op{rel: rel})
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, o := range ops {
		target := filepath.Join(liveD, o.rel)
		if o.replace {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		}
		if o.isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		written, err := replaceFile(filepath.Join(stageD, o.rel), target)
		if err != nil {
			return err
		}
		// Advance only our writes, never a fresh scan that could adopt another device's edits.
		atOpen[o.rel] = written
	}
	// Remove what this session dropped: files it saw at open, unchanged since, and
	// absent from the stage. Folders go once empty.
	var dirs []string
	err = filepath.Walk(liveD, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(liveD, p)
		if err != nil || rel == "." {
			return err
		}
		if _, serr := os.Lstat(filepath.Join(stageD, rel)); !os.IsNotExist(serr) {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if unchanged(rel, info) {
			return os.Remove(p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // stays while it holds another device's entries
	}
	return nil
}

// replaceFile copies src next to dst and renames it into place.
func replaceFile(src, dst string) (stamp, error) {
	in, err := os.Open(src)
	if err != nil {
		return stamp{}, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ddv-")
	if err != nil {
		return stamp{}, err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	var written stamp
	if err == nil {
		var fi os.FileInfo
		fi, err = os.Stat(tmp.Name())
		if err == nil {
			written = stamp{fi.Size(), fi.ModTime()}
		}
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return written, err
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
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "ddvclose-") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
