package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
	"discodrive.org/daemon/internal/safepath"
)

const pageLimit = 500

// errSourceUnavailable marks a failure to fetch content from the server, as opposed to a
// failure to put it on disk. The first says nothing more can be applied right now; the second
// is about one node and leaves the rest of the feed worth applying.
var errSourceUnavailable = errors.New("source unavailable")

// Engine applies server changes to the local root directory.
type Engine struct {
	activity      activityTracker
	observeChange func(Change, string, time.Duration, error)
	src           Source
	idx           *index.Index
	root          string
	prepared      bool

	// bulkDeleteConfirmed lets one push carry deletions past the safety threshold; see
	// [Engine.ConfirmBulkDelete].
	bulkDeleteConfirmed bool

	// setAside is where the first pull moved the folder's previous contents, if it did;
	// see [Engine.SetAside].
	setAside string

	// savedAsConflict maps a local path to the hash of the content a push just had
	// saved on the server as a conflict copy: the pull may replace that file without
	// keeping another local copy.
	savedAsConflict map[string]string
}

func (e *Engine) markSavedAsConflict(localPath, relPath, hash string) {
	if localPath == "" {
		localPath = relPath
	}
	if e.savedAsConflict == nil {
		e.savedAsConflict = map[string]string{}
	}
	e.savedAsConflict[localPath] = hash
}

// ObserveChanges installs a diagnostic callback before the engine starts.
func (e *Engine) ObserveChanges(fn func(Change, string, time.Duration, error)) { e.observeChange = fn }

func New(src Source, idx *index.Index, root string) *Engine {
	return &Engine{src: src, idx: idx, root: root}
}

// NewPrepared uses a directory whose previous contents the host has already
// preserved. It must not rename the root, including after an interrupted first pull.
func NewPrepared(src Source, idx *index.Index, root string) *Engine {
	e := New(src, idx, root)
	e.prepared = true
	return e
}

// PullOnce fetches all changes after the cursor and applies them in order.
//
// A change that cannot be applied does not end the pull: the rest are still applied, and the
// failures are reported together at the end. One unapplicable entry used to stop everything
// behind it — and because the cursor stayed put, every later run failed on the same entry, so
// a phone that met one such file synced nothing at all, permanently.
//
// The cursor advances only across the unbroken run of changes applied from the start, so a
// change that failed is retried on the next pull rather than skipped. Changes after it are
// applied now and re-applied then, which is harmless: applying is idempotent.
func (e *Engine) PullOnce(ctx context.Context) (result error) {
	e.activityBegin("pull", "")
	defer func() { e.activityEnd(result) }()
	if err := e.establishMirror(); err != nil {
		return err
	}
	since, err := e.idx.Cursor()
	if err != nil {
		return err
	}
	var failures []error
	for {
		e.activityBegin("pull", "")
		changes, cursor, hasMore, err := e.src.Changes(ctx, since, pageLimit)
		if err != nil {
			return err
		}
		for _, c := range changes {
			e.activityBegin("pull", c.RelPath)
			started := time.Now()
			if e.observeChange != nil {
				e.observeChange(c, "begin", 0, nil)
			}
			aerr := e.apply(ctx, c)
			e.activityEnd(aerr)
			if e.observeChange != nil {
				e.observeChange(c, "end", time.Since(started), aerr)
			}
			if aerr != nil {
				wrapped := fmt.Errorf("seq %d (%s): %w", c.Seq, c.RelPath, aerr)
				// Reaching the server failed: everything after it would fail the same
				// way, so stop rather than spend a phone's data on 500 doomed
				// downloads. Anything else is about this one node — a name the
				// filesystem rejects, content that did not match its hash — and the
				// rest of the feed is still worth applying.
				if errors.Is(aerr, errSourceUnavailable) {
					return errors.Join(append(failures, wrapped)...)
				}
				failures = append(failures, wrapped)
				continue
			}
			if len(failures) > 0 {
				// Something earlier still has to be retried; leave the cursor behind it.
				continue
			}
			if err := e.idx.SetCursor(c.Seq); err != nil {
				return err
			}
		}
		since = cursor
		if !hasMore {
			// The server's tree has been fetched; from here on the folder is the mirror
			// and what appears in it is the user's work to push. A brand-new account has
			// no changes at all, and this is what lets its first files go up.
			if err := e.idx.SetMirrorReady(true); err != nil {
				return err
			}
			return errors.Join(failures...)
		}
	}
}

// SetAside is the folder the previous contents of the sync root were moved to by the
// first pull, or "" if the root was empty or the mirror was already established. Hosts
// show it to the user once; it is never read by the engine again.
func (e *Engine) SetAside() string { return e.setAside }

// ResetIndexKeepingFiles forgets the tree and re-pulls it on the next pass, but keeps the
// folder as the mirror rather than setting it aside: the recovery for a sync stopped by the
// mass-deletion check, where the files on disk are the right ones and only the index is
// wrong. Files not on the server are then uploaded by the pass after the pull.
func (e *Engine) ResetIndexKeepingFiles() error {
	if err := e.idx.Clear(); err != nil {
		return err
	}
	return e.idx.SetKeepLocalOnce(true)
}

// establishMirror runs before a pull into an index that has never pulled — a pairing, a
// re-pairing, a wiped state database. After pairing the server is the truth: whatever the
// folder holds is the device's previous life, not new files, and it is moved next to the
// root (root.old-<stamp>) untouched, leaving a clean folder for the server's tree. Nothing
// in it is ever uploaded. An empty folder (OS junk aside) needs no moving.
func (e *Engine) establishMirror() error {
	if e.prepared {
		return nil
	}
	ready, err := e.idx.MirrorReady()
	if err != nil || ready {
		return err
	}
	if keep, err := e.idx.KeepLocalOnce(); err != nil {
		return err
	} else if keep {
		return e.idx.SetKeepLocalOnce(false)
	}
	entries, err := os.ReadDir(e.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	occupied := false
	for _, ent := range entries {
		if !isOSJunk(ent.Name()) && !strings.HasPrefix(ent.Name(), ".kf-tmp-") {
			occupied = true
			break
		}
	}
	if !occupied {
		return nil
	}
	if err := refuseToSetAside(e.root); err != nil {
		return err
	}
	base := filepath.Clean(e.root) + ".old-" + time.Now().Format("20060102-150405")
	aside := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(aside); os.IsNotExist(err) {
			break
		}
		aside = fmt.Sprintf("%s-%d", base, n)
	}
	if err := os.Rename(e.root, aside); err != nil {
		return fmt.Errorf("set the previous folder aside: %w", err)
	}
	if err := os.MkdirAll(e.root, 0o755); err != nil {
		return err
	}
	e.setAside = aside
	return nil
}

// refuseToSetAside guards against a sync root that is not a folder of its own — the home
// directory, a volume root — which must never be renamed out from under the user.
func refuseToSetAside(root string) error {
	clean := filepath.Clean(root)
	if clean == filepath.Dir(clean) {
		return fmt.Errorf("sync folder %s is a filesystem root; choose an empty folder of its own", root)
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == clean {
		return errors.New("sync folder is the home directory; choose an empty folder of its own")
	}
	return nil
}

// ScopeEpoch returns the scope epoch the engine last reconciled to.
func (e *Engine) ScopeEpoch() (int64, error) { return e.idx.ScopeEpoch() }

// ResetForScope reconciles the local mirror after the server's sync scope changed: wipe the
// index + cursor, re-pull the (now differently scoped) tree from scratch, then delete local
// files that are no longer in scope. PushLocal is intentionally NOT run — local files were
// mapped to the OLD scope and must not be pushed into the new one. The resulting local data
// loss of the old mirror is expected and is gated behind the web danger-confirm modal.
func (e *Engine) ResetForScope(ctx context.Context, epoch int64) error {
	ready, err := e.idx.MirrorReady()
	if err != nil {
		return err
	}
	knownScope, err := e.idx.HasScopeEpoch()
	if err != nil {
		return err
	}
	if !ready && !knownScope {
		// A first scope is not a rebuild. Record it before pulling so an interrupted
		// initial download resumes normally, without sweeping unrelated local files.
		if err := e.idx.SetMirrorReady(false); err != nil {
			return err
		}
		if err := e.idx.SetScopeEpoch(epoch); err != nil {
			return err
		}
		return e.PullOnce(ctx)
	}
	// Older indexes may not have recorded epoch zero. Persist it before clearing
	// readiness so a failed rebuild cannot be mistaken for a first connection.
	last, err := e.idx.ScopeEpoch()
	if err != nil {
		return err
	}
	if err := e.idx.SetScopeEpoch(last); err != nil {
		return err
	}

	// A scope change is not a pairing: the folder stays where it is and is reconciled,
	// which keeps the in-scope files instead of downloading them all again.
	if err := e.ResetIndexKeepingFiles(); err != nil {
		return err
	}
	if err := e.PullOnce(ctx); err != nil {
		return err
	}
	if err := e.sweepOrphans(); err != nil {
		return err
	}
	return e.idx.SetScopeEpoch(epoch)
}

// removeSynced applies a server-side delete to abs without losing unsynced data: a
// file goes only if it still matches what the index last synced; inside a folder,
// files that never synced or were edited since stay, and so do their folders. The
// index forgets everything under abs, so what stays is uploaded again as new.
func (e *Engine) removeSynced(abs string) error {
	all, err := e.idx.All()
	if err != nil {
		return err
	}
	fi, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return e.forgetUnder(abs, all)
	}
	if err != nil {
		return err
	}
	synced := map[string]string{} // local path → last synced hash
	for _, n := range all {
		if !n.IsDir {
			synced[n.LocalPath] = n.ContentHash
		}
	}
	removeFile := func(p string) error {
		rel, err := filepath.Rel(e.root, p)
		if err != nil {
			return err
		}
		want, known := synced[filepath.ToSlash(rel)]
		if !known {
			return nil
		}
		have, _, err := hashFile(p)
		if err != nil || have != want {
			return err
		}
		return os.Remove(p)
	}
	if !fi.IsDir() {
		if fi.Mode().IsRegular() {
			if err := removeFile(abs); err != nil {
				return err
			}
		}
		return e.forgetUnder(abs, all)
	}
	var dirs []string
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, p)
		case d.Type().IsRegular():
			if isOSJunk(d.Name()) || strings.HasPrefix(d.Name(), ".kf-tmp-") {
				return os.Remove(p)
			}
			return removeFile(p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i]) // fails, as it should, while something unsynced remains
	}
	return e.forgetUnder(abs, all)
}

// forgetUnder drops the index entries (from all) for abs and everything below it.
func (e *Engine) forgetUnder(abs string, all []index.Node) error {
	rel, err := filepath.Rel(e.root, abs)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	for _, n := range all {
		if n.LocalPath == rel || strings.HasPrefix(n.LocalPath, rel+"/") {
			if err := e.idx.Delete(n.NodeID); err != nil {
				return err
			}
		}
	}
	return nil
}

// localConflictTag marks the copies apply keeps of local edits it had to replace.
const localConflictTag = " (conflict, local, "

// localConflictName returns a free "name (conflict, local, <time>).ext" next to abs.
func localConflictName(abs string) (string, error) {
	dir, base := filepath.Split(abs)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	truncate := func(s string, limit int) string {
		if len(s) <= limit {
			return s
		}
		s = s[:limit]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		return s
	}
	ext = truncate(ext, 64)
	stamp := time.Now().Format("2006-01-02 15-04-05")
	for i := 1; i <= 10000; i++ {
		number := ""
		if i > 1 {
			number = fmt.Sprintf(" %d", i)
		}
		suffix := localConflictTag + stamp + number + ")" + ext
		p := filepath.Join(dir, truncate(stem, 255-len(suffix))+suffix)
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("no free conflict name for %q", abs)
}

// sweepOrphans deletes any file/dir under root that is not part of the index. The keep set
// includes every ancestor directory of every indexed node, so directories implicitly
// created for a file (without their own change entry) are never swept away.
func (e *Engine) sweepOrphans() error {
	nodes, err := e.idx.All()
	if err != nil {
		return err
	}
	keep := map[string]bool{".": true}
	for _, n := range nodes {
		for p := filepath.Clean(filepath.FromSlash(n.LocalPath)); p != "." && p != "/" && p != ""; {
			keep[p] = true
			parent := filepath.Dir(p)
			if parent == p {
				break
			}
			p = parent
		}
	}
	var orphans []string
	_ = filepath.WalkDir(e.root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || p == e.root {
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".kf-tmp-") {
			return nil
		}
		rel, rerr := filepath.Rel(e.root, p)
		if rerr != nil {
			return nil
		}
		if strings.Contains(filepath.Base(p), localConflictTag) {
			// A local edit set aside by a pull (see apply): the user's data, not an
			// orphan. It and its folders stay; the next push uploads it.
			for q := rel; q != "." && q != ""; q = filepath.Dir(q) {
				keep[q] = true
			}
			return nil
		}
		if !keep[rel] {
			orphans = append(orphans, p)
		}
		return nil
	})
	// A folder counted as orphan before a conflict copy inside it was reached stays.
	kept := orphans[:0]
	for _, p := range orphans {
		if rel, err := filepath.Rel(e.root, p); err == nil && !keep[rel] {
			kept = append(kept, p)
		}
	}
	orphans = kept
	// Deepest paths first, so a removed subtree never trips up a later removal.
	sort.Slice(orphans, func(i, j int) bool { return len(orphans[i]) > len(orphans[j]) })
	for _, p := range orphans {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

// abs converts a server rel_path to an absolute path and verifies it stays within root.
// Besides the lexical containment check, it rejects paths whose existing components
// include a symlink: os.RemoveAll/MkdirAll/Rename would otherwise follow a symlinked
// directory and read, write, or delete data outside the sync folder. The same guard is
// shared (via internal/safepath) by the desktop and mobile file-write paths.
func (e *Engine) abs(rel string) (string, error) {
	return safepath.Join(e.root, rel)
}

// localPathFor is where a node goes on disk. Usually that is its server path, but a path the
// filesystem would reject is mapped to look-alike characters (see internal/localname). The
// mapping is one-to-one, so a clash is only possible against a server path that already
// contains the look-alikes; a node holding the name keeps it, and the newcomer is suffixed.
func (e *Engine) localPathFor(c Change) (string, error) {
	local := localname.Localize(c.RelPath)
	if local == c.RelPath {
		return local, nil
	}
	holder, taken, err := e.idx.NodeIDByLocalPath(local)
	if err != nil {
		return "", err
	}
	if taken && holder != c.NodeID {
		return localname.Disambiguate(local, c.NodeID), nil
	}
	return local, nil
}

func (e *Engine) apply(ctx context.Context, c Change) error {
	localPath, err := e.localPathFor(c)
	if err != nil {
		return err
	}
	abs, err := e.abs(localPath)
	if err != nil {
		return err
	}

	if c.Deleted {
		// Delete what the index says this node is on disk, not whatever the feed entry
		// names: a reordered or compacted feed must not remove another node's file.
		if n, ok, err := e.idx.Get(c.NodeID); err != nil {
			return err
		} else if ok {
			if abs, err = e.abs(n.LocalPath); err != nil {
				return err
			}
		}
		// Never let an empty / "." / "/" rel_path resolve the delete to the sync root
		// itself: a malicious server could otherwise RemoveAll the whole synced folder.
		if abs == filepath.Clean(e.root) {
			return fmt.Errorf("refusing to delete sync root (rel_path %q)", c.RelPath)
		}
		if err := e.removeSynced(abs); err != nil {
			return err
		}
		return e.idx.Delete(c.NodeID)
	}

	if c.IsDir {
		// A known dir arriving under a new rel_path is a server-side move/rename.
		// Rename the whole subtree on disk in one shot — descendants' own feed
		// entries then find their files already in place and become index-only
		// no-ops. Without this the old dir survived empty, DetectLocal reported it
		// as a local create, and PushLocal resurrected a ghost folder on the server.
		if existing, ok, gerr := e.idx.Get(c.NodeID); gerr != nil {
			return gerr
		} else if ok && existing.LocalPath != localPath {
			if oldAbs, aerr := e.abs(existing.LocalPath); aerr == nil {
				if fi, serr := os.Stat(oldAbs); serr == nil && fi.IsDir() {
					if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
						return err
					}
					// Rename may fail legitimately (e.g. target already exists after
					// a partial manual move) — fall through to MkdirAll; descendants
					// will be applied by their own feed entries.
					_ = os.Rename(oldAbs, abs)
				}
			}
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
		return e.idx.Put(index.Node{NodeID: c.NodeID, RelPath: c.RelPath, LocalPath: localPath, IsDir: true, Version: c.Version})
	}

	existing, ok, err := e.idx.Get(c.NodeID)
	if err != nil {
		return err
	}

	if ok && existing.LocalPath != localPath {
		if oldAbs, aerr := e.abs(existing.LocalPath); aerr == nil {
			if _, serr := os.Stat(oldAbs); serr == nil {
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return err
				}
				if err := os.Rename(oldAbs, abs); err != nil {
					return err
				}
			}
		}
	}

	if ok && c.ContentHash != "" && existing.ContentHash == c.ContentHash {
		if _, serr := os.Stat(abs); serr == nil {
			return e.idx.Put(index.Node{NodeID: c.NodeID, RelPath: c.RelPath, LocalPath: localPath,
				Version: c.Version, ContentHash: c.ContentHash, Size: c.Size})
		}
	}

	// The feed echoing the version this device itself uploaded: the local file is that
	// upload or newer. Record the server's hash and keep the file — if the upload was
	// torn by a concurrent edit, the hashes differ and the next push sends it again.
	if ok && c.Version > 0 && c.Version == existing.Version && existing.LocalPath == localPath {
		if _, serr := os.Stat(abs); serr == nil {
			return e.idx.Put(index.Node{NodeID: c.NodeID, RelPath: c.RelPath, LocalPath: localPath,
				Version: c.Version, ContentHash: c.ContentHash, Size: c.Size})
		}
	}

	// Before replacing a local file, make sure nothing on it would be lost: it must be
	// what the index last synced, what the server now sends, or content a push already
	// saved as a conflict copy. Anything else — an edit not pushed yet, a file kept by
	// a "keep local files" reset — is kept next to it as a local conflict copy.
	if fi, serr := os.Lstat(abs); serr == nil && fi.Mode().IsRegular() {
		have, _, herr := hashFile(abs)
		if herr != nil {
			return herr
		}
		switch {
		case c.ContentHash != "" && have == c.ContentHash:
			return e.idx.Put(index.Node{NodeID: c.NodeID, RelPath: c.RelPath, LocalPath: localPath,
				Version: c.Version, ContentHash: c.ContentHash, Size: c.Size})
		case ok && have == existing.ContentHash:
		case e.savedAsConflict[localPath] == have:
		default:
			conflict, err := localConflictName(abs)
			if err != nil {
				return err
			}
			if err := os.Rename(abs, conflict); err != nil {
				return err
			}
		}
		delete(e.savedAsConflict, localPath)
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".kf-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	h := sha256.New()
	if derr := e.src.Download(ctx, c.NodeID, io.MultiWriter(tmp, h)); derr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("%w: %w", errSourceUnavailable, derr)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if c.ContentHash != "" && hex.EncodeToString(h.Sum(nil)) != c.ContentHash {
		os.Remove(tmpName)
		return fmt.Errorf("downloaded content hash mismatch")
	}
	if err := os.Rename(tmpName, abs); err != nil {
		os.Remove(tmpName)
		return err
	}
	return e.idx.Put(index.Node{NodeID: c.NodeID, RelPath: c.RelPath, LocalPath: localPath,
		Version: c.Version, ContentHash: c.ContentHash, Size: c.Size})
}
