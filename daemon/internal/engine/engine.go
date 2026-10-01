package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

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

	// scopeBefore is the index as it stood before a scope reset began, kept until the
	// reset completes: a retry after a failed pull finds the index already cleared, and
	// the sweep still needs to know which files were synced. The index keeps the same
	// snapshot on disk (index.BeforeReset) for a retry after a restart.
	scopeBefore    []index.Node
	scopeResetting bool
	// pullingForReset is set while ResetForScope runs its pull. Any other pull that
	// completes means the folder was reconciled outside a reset (the scope went back to
	// the one it had), and the snapshot of a failed reset no longer describes it.
	pullingForReset bool

	// folded maps a case- and normalization-folded local path to the node holding it,
	// built once per pull (see [Engine.localPathFor]). Entries can go stale within the
	// pull and are checked against the index before they are trusted.
	folded map[string]foldedHolder
	// dirLocal maps a folder's server path to where the index has it on disk, built
	// and checked the same way as folded.
	dirLocal map[string]foldedHolder

	// exists caches the server's answers to NodeExists for one pull (see
	// [Engine.presenceOf]); nil outside a pull.
	exists map[string]presence

	// pushSeq is the seq the current push pass records for the rows it writes; 0 until
	// the pass's first write (see [Engine.putPushed]).
	pushSeq int64
}

type foldedHolder struct{ nodeID, localPath string }

// removeFile removes a synced file a server delete doomed; tests swap it to inject failures.
var removeFile = os.Remove

// foldPath is the key two local paths share when a case-insensitive or
// normalization-insensitive filesystem (APFS, NTFS, Android shared storage) would
// resolve them to the same file.
func foldPath(p string) string { return strings.ToLower(norm.NFC.String(p)) }

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
	e.folded, e.dirLocal = nil, nil // rebuilt from the index on the first change of this pull
	e.exists = map[string]presence{}
	defer func() { e.folded, e.dirLocal, e.exists = nil, nil, nil }()
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
			if len(failures) == 0 && !e.pullingForReset {
				e.scopeBefore, e.scopeResetting = nil, false
				if err := e.idx.ClearBeforeReset(); err != nil {
					return err
				}
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
//
// The cleared rows are kept as the pre-reset snapshot (index.ClearKeepingSnapshot) until a
// scope reset or a completed pull shows the folder reconciled.
func (e *Engine) ResetIndexKeepingFiles() error {
	if err := e.idx.ClearKeepingSnapshot(); err != nil {
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
// index + cursor, re-pull the (now differently scoped) tree from scratch, then delete the
// local copies of server files that fell out of scope. PushLocal is intentionally NOT run in
// this pass. Only files the old index knew, still holding exactly what was last synced, are
// deleted: their content is on the server. Anything else — a note written offline, an edit
// not pushed yet — is the user's only copy and stays, to be uploaded by a later pass.
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

	// What the folder held as synced before the reset: the sweep may only remove files
	// whose content this proves is on the server. A retry reuses the first attempt's
	// snapshot, taken before that attempt cleared the index: from memory, or after a
	// restart from the copy the index kept when it was cleared. Live rows at that point
	// are a partial re-pull and never replace it.
	if !e.scopeResetting {
		before, err := e.idx.BeforeReset()
		if err != nil {
			return err
		}
		if len(before) == 0 {
			if before, err = e.idx.All(); err != nil {
				return err
			}
		}
		e.scopeBefore, e.scopeResetting = before, true
	}

	// A scope change is not a pairing: the folder stays where it is and is reconciled,
	// which keeps the in-scope files instead of downloading them all again.
	if err := e.ResetIndexKeepingFiles(); err != nil {
		return err
	}
	e.pullingForReset = true
	err = e.PullOnce(ctx)
	e.pullingForReset = false
	if err != nil {
		// The pull spent the one-shot keep-local allowance. Re-arm it: if the scope goes
		// back before the retry, the ordinary pass that follows must keep this folder as
		// the mirror rather than set it all aside. A pull that reached the end of the
		// feed has established the mirror already and needs no allowance.
		if ready, rerr := e.idx.MirrorReady(); rerr == nil && !ready {
			_ = e.idx.SetKeepLocalOnce(true)
		}
		return err
	}
	if err := e.sweepOrphans(e.scopeBefore); err != nil {
		return err
	}
	if err := e.idx.SetScopeEpoch(epoch); err != nil {
		return err
	}
	e.scopeBefore, e.scopeResetting = nil, false
	return e.idx.ClearBeforeReset()
}

// removeDeleted applies a server-side delete of node n by identity, never by whatever
// happens to sit at or under its local path. Paths are not unique in the index: a ghost
// (gone on the server, its delete not delivered yet) can share a server or local path with
// the live node that took it since. The rivals of n are the rows at n's server path or at
// n's local path (compared as the disk does, see samePlace). A rival is live, and n a
// ghost, when it was placed after n (a higher seq, see Index.SetSeq and putPushed) or, on
// a seq tie, when the server says it still has it (see presenceOf).
//
//   - A rival is live: n is a ghost. If a rival records n's local path, what is there is
//     the rival's and only n's row goes. Otherwise n's own file (if it still holds its
//     synced hash) or folder goes. Rows below n's folder that were placed after the live
//     rival, or whose server path is outside n's, are the live tree's: they move out
//     (relocateStray) before the folder goes. The rest below it cannot be told apart from
//     n's own children, and the server sends a delete row of their own for every node of
//     a purged subtree.
//   - No rival is live: n, its rivals (ghost rows at its paths, which then have nothing
//     left to delete) and everything under their server paths are doomed (removeDoomed).
//   - A stray — a node under a doomed folder on disk whose server path is elsewhere, a
//     placement from an older version — is not n's. It moves, a folder as a whole, to
//     where its own server folder is (relocateStray), so the doomed folder can go. One
//     that cannot move keeps its place; the folder holding it stays on disk without a
//     row. A row kept there would outlive the server folder and later re-route the user's
//     new files, or delete a live server folder of the same path when the user removes
//     the local one.
//   - Afterwards, folders above what was removed that are left empty and that no row
//     records go too (removeEmptyAbove).
//   - A doomed node whose local path is not a path inside the root keeps its row.
func (e *Engine) removeDeleted(ctx context.Context, n index.Node, abs string) error {
	all, err := e.idx.All()
	if err != nil {
		return err
	}
	seqs, err := e.idx.Seqs()
	if err != nil {
		return err
	}
	under := func(p, dir string) bool { return strings.HasPrefix(p, dir+"/") }

	var rivals []index.Node
	for _, o := range all {
		if o.NodeID != n.NodeID && (o.RelPath == n.RelPath || e.samePlace(o.LocalPath, n.LocalPath)) {
			rivals = append(rivals, o)
		}
	}
	ns := seqs[n.NodeID]
	var live *index.Node
	for i, r := range rivals {
		if rs := seqs[r.NodeID]; rs > ns && (live == nil || rs > seqs[live.NodeID]) {
			live = &rivals[i]
		}
	}
	if live == nil {
		undecided := false
		for i, r := range rivals {
			if seqs[r.NodeID] != ns {
				continue
			}
			p, err := e.presenceOf(ctx, r.NodeID)
			if err != nil {
				return err
			}
			if p == presenceLive {
				live = &rivals[i]
				break
			}
			undecided = undecided || p == presenceUnknown
		}
		if live == nil && undecided {
			// The server could not be asked: never guess. Only n's row goes; the disk is
			// left alone.
			return e.idx.Delete(n.NodeID)
		}
	}
	if live != nil {
		// n is a ghost. What is at n's place belongs to the rival there if that rival is
		// live, or may be (a tie the server could not settle): only n's row goes. A rival
		// there that is older than n, or that the server no longer has, is a ghost like n,
		// and goes with it.
		var placeGone []index.Node
		for _, r := range rivals {
			if !e.samePlace(r.LocalPath, n.LocalPath) {
				continue
			}
			holds := seqs[r.NodeID] > ns
			if seqs[r.NodeID] == ns {
				p, err := e.presenceOf(ctx, r.NodeID)
				if err != nil {
					return err
				}
				holds = p != presenceGone
			}
			if !holds {
				placeGone = append(placeGone, r)
				continue
			}
			// A spelling of it that differs only in case (a legacy row on a
			// case-insensitive disk) takes the live row's spelling, or the scan would read
			// it as a new file and the live one as deleted.
			if r.LocalPath != n.LocalPath {
				if to, err := e.abs(r.LocalPath); err == nil {
					_ = os.Rename(abs, to)
				}
			}
			return e.idx.Delete(n.NodeID)
		}
		if n.IsDir {
			var theirs []index.Node
			for _, o := range all {
				if o.NodeID != n.NodeID && under(o.LocalPath, n.LocalPath) &&
					(!under(o.RelPath, n.RelPath) || seqs[o.NodeID] > seqs[live.NodeID]) {
					theirs = append(theirs, o)
				}
			}
			if err := e.relocateStrays(theirs, []string{n.LocalPath}); err != nil {
				return err
			}
		}
		return e.removeDoomed(append([]index.Node{n}, placeGone...), abs, n.NodeID)
	}

	// n is the live one.
	roots := append([]index.Node{n}, rivals...)
	doomedIDs := map[string]bool{}
	for _, o := range all {
		for _, r := range roots {
			if o.NodeID == r.NodeID || (r.IsDir && under(o.RelPath, r.RelPath)) {
				doomedIDs[o.NodeID] = true
				break
			}
		}
	}
	var doomedDirs []string
	for _, o := range all {
		if doomedIDs[o.NodeID] && o.IsDir {
			doomedDirs = append(doomedDirs, o.LocalPath)
		}
	}
	var strays []index.Node
	for _, o := range all {
		if doomedIDs[o.NodeID] {
			continue
		}
		for _, d := range doomedDirs {
			if under(o.LocalPath, d) {
				strays = append(strays, o)
				break
			}
		}
	}
	if err := e.relocateStrays(strays, doomedDirs); err != nil {
		return err
	}
	// Relocation can carry doomed rows along (inside a stray folder): read them afresh.
	all, err = e.idx.All()
	if err != nil {
		return err
	}
	var doomed []index.Node
	for _, o := range all {
		if doomedIDs[o.NodeID] {
			doomed = append(doomed, o)
		}
	}
	return e.removeDoomed(doomed, abs, n.NodeID)
}

// presence is what the server says of a node: it still has it, it does not, or it could
// not say.
type presence int

const (
	presenceUnknown presence = iota
	presenceLive
	presenceGone
)

// presenceOf asks the server whether it still has nodeID. It settles a seq tie between a
// deleted node and a rival: legacy rows all have seq 0, and nothing the client recorded
// orders them (the rowid is the order rows were learned in, not the order they were moved
// in, so a node moved onto a ghost's path can be older than the ghost). The server does
// know: a rival it still has is live; one it answers "not found" for is a ghost.
//
//   - A source that cannot be asked (tests, a host without the check) gives
//     presenceUnknown, and so does a server answer that is neither "not found" nor a
//     temporary failure: a 4xx such as a proxy or firewall refusing GET /files/{id}.
//     Callers then remove nothing on disk. Holding the cursor on such an answer would stop
//     the whole feed behind one tie, for as long as the refusal lasts.
//   - A temporary failure — the network, a 5xx, 429, a rejected certificate — is returned
//     as an error of the source: the pull stops with the cursor held and the retry decides.
//     It is never guessed.
//
// Each node is asked at most once per pull; failures are not kept. Only rivals are asked,
// never their descendants.
func (e *Engine) presenceOf(ctx context.Context, nodeID string) (presence, error) {
	checker, ok := e.src.(NodeChecker)
	if !ok {
		return presenceUnknown, nil
	}
	if p, ok := e.exists[nodeID]; ok {
		return p, nil
	}
	p := presenceGone
	exists, err := checker.NodeExists(ctx, nodeID)
	switch {
	case err != nil && refusedAnswer(err):
		log.Printf("engine: cannot tell whether node %s still exists (%v); leaving the disk as it is", nodeID, err)
		p = presenceUnknown
	case err != nil:
		return presenceUnknown, fmt.Errorf("%w: asking whether node %s still exists: %w", errSourceUnavailable, nodeID, err)
	case exists:
		p = presenceLive
	}
	if e.exists != nil {
		e.exists[nodeID] = p
	}
	return p, nil
}

// refusedAnswer reports whether err is an HTTP answer that retrying will not change soon:
// any status but 5xx and 429. Errors without a status (the network, TLS) are not.
func refusedAnswer(err error) bool {
	var hs interface{ HTTPStatus() int }
	if !errors.As(err, &hs) {
		return false
	}
	code := hs.HTTPStatus()
	return code != http.StatusTooManyRequests && code < 500
}

// samePlace reports whether local paths a and b name one file or folder on disk: the same
// path, or two spellings that differ only in case or normalization and that the disk
// resolves to one entry (a case-insensitive disk).
func (e *Engine) samePlace(a, b string) bool {
	if a == b {
		return true
	}
	if foldPath(a) != foldPath(b) {
		return false
	}
	pa, err := e.abs(a)
	if err != nil {
		return false
	}
	pb, err := e.abs(b)
	if err != nil {
		return false
	}
	fa, err := os.Lstat(pa)
	if err != nil {
		return false
	}
	fb, err := os.Lstat(pb)
	return err == nil && os.SameFile(fa, fb)
}

// relocateStrays moves each of the given rows that sits on disk under one of the dirs
// (folders a server delete removes) to where localPathFor would place it: in its server
// folder as the index has it, or at the root for a top-level node. A folder moves as a
// whole, with the rows below it. Nothing is overwritten and no folder is created: when the
// server folder is not indexed or not on disk, when it is itself under one of the dirs,
// when no free name is found, or when the rename fails, the row stays where it is. A
// taken name gets a generated one.
func (e *Engine) relocateStrays(strays []index.Node, dirs []string) error {
	if len(strays) == 0 {
		return nil
	}
	if err := e.loadPullMaps(); err != nil {
		return err
	}
	// Top-most first: rows inside a folder that moves go with it.
	sort.Slice(strays, func(i, j int) bool { return len(strays[i].LocalPath) < len(strays[j].LocalPath) })
	var tried []string
	for _, o := range strays {
		inside := false
		for _, t := range tried {
			if strings.HasPrefix(o.LocalPath, t+"/") {
				inside = true
				break
			}
		}
		if inside {
			continue
		}
		if o.IsDir {
			tried = append(tried, o.LocalPath)
		}
		if err := e.relocateStray(o, dirs); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) relocateStray(o index.Node, dirs []string) error {
	from, err := e.abs(o.LocalPath)
	if err != nil {
		return nil
	}
	fi, err := os.Lstat(from)
	if err != nil || !(fi.Mode().IsRegular() || (o.IsDir && fi.IsDir())) {
		return nil // nothing of it holds the folder
	}
	var target string
	if parentRel := path.Dir(o.RelPath); parentRel == "." || parentRel == "/" || parentRel == "" {
		target = localname.Localize(o.RelPath)
	} else {
		parentLocal, ok, err := e.parentLocalPath(parentRel)
		if err != nil || !ok {
			return err // its server folder is not indexed: creating it would push it
		}
		pp, err := e.abs(parentLocal)
		if err != nil {
			return nil
		}
		if pfi, err := os.Lstat(pp); err != nil || !pfi.IsDir() {
			return nil
		}
		target = parentLocal + "/" + localname.Localize(path.Base(o.RelPath))
	}
	if foldPath(target) == foldPath(o.LocalPath) {
		return nil // already where it belongs
	}
	for _, d := range dirs {
		if fd := foldPath(d); foldPath(target) == fd || strings.HasPrefix(foldPath(target), fd+"/") {
			return nil // its folder is going too
		}
	}
	free := func(cand string) (string, bool, error) {
		clash, err := e.foldedClash(o.NodeID, cand)
		if err != nil || clash {
			return "", false, err
		}
		p, err := e.abs(cand)
		if err != nil {
			return "", false, nil
		}
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return "", false, nil
		}
		return p, true, nil
	}
	to, ok, err := free(target)
	for i := 0; !ok && err == nil && i < 16; i++ {
		cand := disambiguated(target, o.NodeID, i)
		if to, ok, err = free(cand); ok {
			target = cand
		}
	}
	if err != nil || !ok {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return nil
	}
	moved := []index.Node{o}
	if o.IsDir {
		all, err := e.idx.All()
		if err != nil {
			return err
		}
		for _, r := range all {
			if strings.HasPrefix(r.LocalPath, o.LocalPath+"/") {
				moved = append(moved, r)
			}
		}
	}
	for _, r := range moved {
		r.LocalPath = target + strings.TrimPrefix(r.LocalPath, o.LocalPath)
		if err := e.idx.Put(r); err != nil {
			return err
		}
		e.folded[foldPath(r.LocalPath)] = foldedHolder{r.NodeID, r.LocalPath}
		if r.IsDir {
			e.dirLocal[r.RelPath] = foldedHolder{r.NodeID, r.LocalPath}
		}
	}
	return nil
}

// removeDoomed removes the doomed nodes' rows and, where nothing would be lost, their files
// and folders (see removeDeleted); abs is where the node with id self is on disk.
//
// A file that cannot be read or removed stays, with its row, and so do the rows of the
// doomed folders it is in and self's row: the error is returned, the pull keeps its cursor
// behind this delete, and the retry finds what is left to do. Self's row goes last, only
// once everything else has; without it the retry would be a no-op.
func (e *Engine) removeDoomed(doomed []index.Node, abs, self string) error {
	all, err := e.idx.All()
	if err != nil {
		return err
	}
	doomedIDs := map[string]bool{}
	for _, d := range doomed {
		doomedIDs[d.NodeID] = true
	}
	// A place another, kept node records is that node's.
	keptByFold := map[string][]string{}
	for _, o := range all {
		if !doomedIDs[o.NodeID] {
			keptByFold[foldPath(o.LocalPath)] = append(keptByFold[foldPath(o.LocalPath)], o.LocalPath)
		}
	}
	kept := func(local string) bool {
		for _, k := range keptByFold[foldPath(local)] {
			if e.samePlace(k, local) {
				return true
			}
		}
		return false
	}
	var errs []error
	var failed []string // local paths of files that had to stay
	type dir struct {
		node index.Node
		abs  string
	}
	var dirs []dir
	var removed []string // local paths gone, or meant to be, for removeEmptyAbove
	for _, d := range doomed {
		p := abs
		if d.NodeID != self {
			if p, err = e.abs(d.LocalPath); err != nil {
				continue // not a path inside the root: its row stays, and so does the disk
			}
		}
		if !kept(d.LocalPath) {
			if d.IsDir {
				dirs = append(dirs, dir{d, p})
				continue // its row goes once its children are handled
			}
			if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
				have, _, err := hashFile(p)
				if err == nil && have == d.ContentHash {
					if err = removeFile(p); os.IsNotExist(err) {
						err = nil
					}
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", d.LocalPath, err))
					failed = append(failed, d.LocalPath)
					continue // the file and its row stay for the retry
				}
			}
			removed = append(removed, d.LocalPath)
		}
		if d.NodeID != self {
			if err := e.idx.Delete(d.NodeID); err != nil {
				return err
			}
		}
	}
	// Deepest first, so a folder emptied by its subfolders' removal goes too.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].abs) > len(dirs[j].abs) })
	for _, d := range dirs {
		if entries, err := os.ReadDir(d.abs); err == nil {
			for _, ent := range entries {
				if ent.Type().IsRegular() && (isOSJunk(ent.Name()) || strings.HasPrefix(ent.Name(), ".kf-tmp-")) {
					os.Remove(filepath.Join(d.abs, ent.Name()))
				}
			}
		}
		os.Remove(d.abs) // fails, as it should, while something else remains
		holdsFailed := false
		for _, f := range failed {
			if strings.HasPrefix(f, d.node.LocalPath+"/") {
				holdsFailed = true
				break
			}
		}
		if holdsFailed {
			continue
		}
		removed = append(removed, d.node.LocalPath)
		if d.node.NodeID != self {
			if err := e.idx.Delete(d.node.NodeID); err != nil {
				return err
			}
		}
	}
	if len(errs) == 0 {
		if err := e.idx.Delete(self); err != nil {
			return err
		}
	}
	if err := e.removeEmptyAbove(removed); err != nil {
		return err
	}
	return errors.Join(errs...)
}

// removeEmptyAbove removes the folders above the given local paths, up to the root, that
// are left empty (OS junk and stale temp files aside) and that no row records: a folder of
// a ghost whose own delete came while its children were in it (the server writes purge
// rows parents first). Left there, the push would create it on the server as a new
// folder, or re-point the live folder of the same server path at it. A folder a row
// records, or that holds anything else, stays, and so does everything above it.
func (e *Engine) removeEmptyAbove(locals []string) error {
	if len(locals) == 0 {
		return nil
	}
	all, err := e.idx.All()
	if err != nil {
		return err
	}
	recorded := map[string]bool{}
	for _, o := range all {
		recorded[foldPath(o.LocalPath)] = true
	}
	for _, l := range locals {
		for d := path.Dir(l); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if recorded[foldPath(d)] {
				break
			}
			p, err := e.abs(d)
			if err != nil {
				break
			}
			removeIfOnlyJunk(p)
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				break
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

// sweepOrphans removes what fell out of scope after a reset: a regular file goes only when
// the fresh index does not have it, the pre-reset index (before) did, and it still holds the
// content last synced — server data the server no longer sends. Everything else stays with
// its folders: files never synced, files edited since, conflict copies. A folder goes only
// when it is left empty and nothing in the fresh index lives in or under it.
func (e *Engine) sweepOrphans(before []index.Node) error {
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
	synced := make(map[string]string, len(before)) // local path → last synced hash
	for _, n := range before {
		if !n.IsDir {
			synced[filepath.Clean(filepath.FromSlash(n.LocalPath))] = n.ContentHash
		}
	}
	var files, dirs []string
	_ = filepath.WalkDir(e.root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || p == e.root {
			return nil
		}
		rel, rerr := filepath.Rel(e.root, p)
		if rerr != nil || keep[rel] {
			return nil
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, p)
		case d.Type().IsRegular():
			if want, known := synced[rel]; known && want != "" {
				files = append(files, p)
			}
		}
		return nil
	})
	for _, p := range files {
		rel, _ := filepath.Rel(e.root, p)
		have, _, err := hashFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if have != synced[rel] {
			continue // edited since the last sync: the only copy of that edit
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// Deepest first, so a folder emptied by removing its subfolders goes too. A folder
	// holding anything kept fails to go, as it should.
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, p := range dirs {
		removeIfOnlyJunk(p)
	}
	return nil
}

// removeIfOnlyJunk removes dir if it holds nothing but OS junk and stale temp files.
func removeIfOnlyJunk(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.Type().IsRegular() || !(isOSJunk(ent.Name()) || strings.HasPrefix(ent.Name(), ".kf-tmp-")) {
			return
		}
	}
	for _, ent := range entries {
		os.Remove(filepath.Join(dir, ent.Name()))
	}
	os.Remove(dir)
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
//
// Two server names can also be one name on disk: "Foo.md" and "foo.md" on a case-insensitive
// filesystem, or the NFC and NFD spellings of "é" on APFS. The index is keyed by the exact
// path, so the second node would land on the first one's file, push its bytes aside as a
// conflict copy, and the next push would read that as a rename of the first — on the server,
// for every device. The newcomer gets a distinct local name instead, like a clashing
// localized name does; its server name is unchanged.
func (e *Engine) localPathFor(c Change) (string, error) {
	if err := e.loadPullMaps(); err != nil {
		return "", err
	}
	// A node whose server path is unchanged stays where it is: its local name was chosen
	// against the nodes present then, and moving it now that a clash is gone would leave
	// its descendants' indexed paths behind. The exception is a node placed outside its
	// folder, because the folder was not known yet when it arrived: it moves into it.
	if n, ok, err := e.idx.Get(c.NodeID); err != nil {
		return "", err
	} else if ok && n.RelPath == c.RelPath && n.LocalPath != "" {
		inPlace := true
		if parentRel := path.Dir(c.RelPath); parentRel != "." && parentRel != "/" && parentRel != "" {
			parentLocal, known, err := e.parentLocalPath(parentRel)
			if err != nil {
				return "", err
			}
			inPlace = !known || strings.HasPrefix(n.LocalPath, parentLocal+"/")
		}
		if inPlace {
			return n.LocalPath, nil
		}
	}
	local := localname.Localize(c.RelPath)
	// A child lives inside its parent folder as the index has it, which differs from the
	// localized server path when that folder was disambiguated. The feed is the server's
	// change log in seq order, each entry carrying the node's current path, so a folder's
	// first entry precedes those of anything created in it; a parent the index does not
	// know (outside the scope, or its entry failed) falls back to the localized path.
	if parentRel := path.Dir(c.RelPath); parentRel != "." && parentRel != "/" && parentRel != "" {
		parentLocal, ok, err := e.parentLocalPath(parentRel)
		if err != nil {
			return "", err
		}
		if ok {
			local = parentLocal + "/" + localname.Localize(path.Base(c.RelPath))
		}
	}
	if localname.Localize(c.RelPath) != c.RelPath {
		holder, taken, err := e.idx.NodeIDByLocalPath(local)
		if err != nil {
			return "", err
		}
		if taken && holder != c.NodeID {
			return e.freeLocalName(c.NodeID, local)
		}
	}
	clash, err := e.foldedClash(c.NodeID, local)
	if err != nil {
		return "", err
	}
	if clash {
		return e.freeLocalName(c.NodeID, local)
	}
	return local, nil
}

// disambiguated is the i-th name tried for nodeID when local is taken: i = 0 is the plain
// [localname.Disambiguate] name, later ones mix a counter into the id.
func disambiguated(local, nodeID string, i int) string {
	if i == 0 {
		return localname.Disambiguate(local, nodeID)
	}
	return localname.Disambiguate(local, nodeID+"#"+strconv.Itoa(i))
}

// freeLocalName returns the first generated name for nodeID that no other node holds, not
// even in another case or normalization: a server name can be exactly the name generated
// for another node, and that node may have arrived first. The sequence depends only on
// local and nodeID, so every device picks the same name for the same index.
func (e *Engine) freeLocalName(nodeID, local string) (string, error) {
	for i := 0; ; i++ {
		cand := disambiguated(local, nodeID, i)
		clash, err := e.foldedClash(nodeID, cand)
		if err != nil {
			return "", err
		}
		if !clash {
			return cand, nil
		}
	}
}

// isGeneratedFrom reports whether cand is one of the names [Engine.freeLocalName] gives
// nodeID when local is taken.
func isGeneratedFrom(cand, local, nodeID string) bool {
	for i := 0; i < 16; i++ {
		if cand == disambiguated(local, nodeID, i) {
			return true
		}
	}
	return false
}

// loadPullMaps builds, once per pull, the lookups localPathFor needs: local paths by their
// folded form, and folders' local paths by server path.
func (e *Engine) loadPullMaps() error {
	if e.folded != nil {
		return nil
	}
	all, err := e.idx.All()
	if err != nil {
		return err
	}
	seqs, err := e.idx.DirSeqs()
	if err != nil {
		return err
	}
	e.folded = make(map[string]foldedHolder, len(all))
	e.dirLocal = map[string]foldedHolder{}
	for _, n := range all {
		e.folded[foldPath(n.LocalPath)] = foldedHolder{n.NodeID, n.LocalPath}
		if !n.IsDir {
			continue
		}
		// Two folders can share a server path: a ghost (gone on the server, its delete not
		// applied yet) and the live folder that took the path since. Children go into the
		// live one, never into the ghost, whose late delete would take them: the one the
		// feed placed at that path last. Rows without a seq (written before it was
		// recorded, or only by a push) fall back to the local names: the folder that
		// arrived while the other held the name got a name generated from it.
		if prev, ok := e.dirLocal[n.RelPath]; ok {
			ps, ns := seqs[prev.nodeID], seqs[n.NodeID]
			if ps > ns || (ps == ns && isGeneratedFrom(prev.localPath, n.LocalPath, prev.nodeID)) {
				continue
			}
		}
		e.dirLocal[n.RelPath] = foldedHolder{n.NodeID, n.LocalPath}
	}
	return nil
}

// parentLocalPath is where the folder at server path rel lives on disk, if the index
// knows it.
func (e *Engine) parentLocalPath(rel string) (string, bool, error) {
	h, ok := e.dirLocal[rel]
	if !ok {
		return "", false, nil
	}
	if h.localPath == localname.Localize(rel) {
		return h.localPath, true, nil // the default placement: nothing to go stale
	}
	// The entry may be stale: the folder deleted or moved earlier in this pull.
	n, ok, err := e.idx.Get(h.nodeID)
	if err != nil || !ok || n.RelPath != rel || n.LocalPath != h.localPath {
		return "", false, err
	}
	return h.localPath, true, nil
}

// foldedClash reports whether another node already holds local, or a local path that
// differs from it only in case or Unicode normalization. An exact holder matters too: a
// server name can be exactly the name generated for another node (see Disambiguate).
func (e *Engine) foldedClash(nodeID, local string) (bool, error) {
	h, ok := e.folded[foldPath(local)]
	if !ok || h.nodeID == nodeID {
		return false, nil
	}
	// The entry may be stale: the holder deleted or moved earlier in this pull.
	n, ok, err := e.idx.Get(h.nodeID)
	if err != nil {
		return false, err
	}
	return ok && n.LocalPath == h.localPath, nil
}

// noteLocalPath records where an applied node now lives for [Engine.localPathFor].
func (e *Engine) noteLocalPath(c Change) {
	if e.folded == nil || c.Deleted {
		return
	}
	if n, ok, err := e.idx.Get(c.NodeID); err == nil && ok {
		e.folded[foldPath(n.LocalPath)] = foldedHolder{n.NodeID, n.LocalPath}
		if n.IsDir {
			e.dirLocal[n.RelPath] = foldedHolder{n.NodeID, n.LocalPath}
		}
	}
}

// sameFile reports whether p is the file fi describes.
func sameFile(fi os.FileInfo, p string) bool {
	other, err := os.Lstat(p)
	return err == nil && os.SameFile(fi, other)
}

// keepUnsyncedAt moves a regular file at abs out of the way (to a local conflict copy)
// before a server rename puts oldAbs there, unless nothing on it would be lost: it holds
// the node's last synced content, the content the server now sends, or content a push
// already saved on the server as a conflict copy. A target that is the very file being
// renamed — a case-only rename on a case-insensitive disk — is left alone.
func (e *Engine) keepUnsyncedAt(abs, oldAbs, syncedHash, newHash, localPath string) error {
	fi, err := os.Lstat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	if ofi, err := os.Lstat(oldAbs); err == nil && os.SameFile(fi, ofi) {
		return nil
	}
	have, _, err := hashFile(abs)
	if err != nil {
		return err
	}
	if (syncedHash != "" && have == syncedHash) || (newHash != "" && have == newHash) {
		return nil
	}
	if saved, ok := e.savedAsConflict[localPath]; ok && saved == have {
		return nil
	}
	conflict, err := localConflictName(abs)
	if err != nil {
		return err
	}
	return os.Rename(abs, conflict)
}

func (e *Engine) apply(ctx context.Context, c Change) error {
	if err := e.applyChange(ctx, c); err != nil {
		return err
	}
	if !c.Deleted {
		// Which of two folders indexed at one server path is live: the one placed last.
		if err := e.idx.SetSeq(c.NodeID, c.Seq); err != nil {
			return err
		}
	}
	e.noteLocalPath(c)
	return nil
}

// applyDelete applies a server-side delete. It deletes what the index says this node is on
// disk, not whatever the feed entry names: a reordered or compacted feed must not remove
// another node's file. A node the index never had has nothing on disk to delete.
//
// A node whose own local path the root rejects (a symlinked component) keeps its row and
// the disk is left alone, like any doomed node's (see removeDeleted). Failing instead would
// hold the cursor behind this entry on every pull, forever.
func (e *Engine) applyDelete(ctx context.Context, c Change) error {
	n, ok, err := e.idx.Get(c.NodeID)
	if err != nil {
		return err
	}
	var abs string
	if ok {
		if abs, err = e.abs(n.LocalPath); err != nil {
			log.Printf("engine: server deleted %s, but its local path %q is not inside the sync folder; left as is: %v", c.NodeID, n.LocalPath, err)
			return nil
		}
	} else {
		localPath, err := e.localPathFor(c)
		if err != nil {
			return err
		}
		if abs, err = e.abs(localPath); err != nil {
			return err
		}
	}
	// Never let an empty / "." / "/" rel_path resolve the delete to the sync root itself:
	// a malicious server could otherwise RemoveAll the whole synced folder.
	if abs == filepath.Clean(e.root) {
		return fmt.Errorf("refusing to delete sync root (rel_path %q)", c.RelPath)
	}
	if !ok {
		return nil
	}
	return e.removeDeleted(ctx, n, abs)
}

func (e *Engine) applyChange(ctx context.Context, c Change) error {
	if c.Deleted {
		return e.applyDelete(ctx, c)
	}
	localPath, err := e.localPathFor(c)
	if err != nil {
		return err
	}
	abs, err := e.abs(localPath)
	if err != nil {
		return err
	}
	// An empty / "." / "/" rel_path names the sync root itself. Nothing may be written
	// there — the temp file would land in the root's parent, outside the sync folder.
	if abs == filepath.Clean(e.root) {
		return fmt.Errorf("refusing to write sync root (rel_path %q)", c.RelPath)
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
					// rename(2) replaces an empty folder at the target, and a file there
					// is someone's data: a folder already there is left alone (the
					// descendants' own feed entries fill it), a file is kept aside.
					if err := e.keepUnsyncedAt(abs, oldAbs, "", c.ContentHash, localPath); err != nil {
						return err
					}
					// A case-only rename on a case-insensitive disk finds the folder
					// itself at the target, and must still rename it.
					if fi, err := os.Lstat(abs); err != nil || !fi.IsDir() || sameFile(fi, oldAbs) {
						// Rename may fail legitimately (e.g. a partial manual move) —
						// fall through to MkdirAll; descendants will be applied by
						// their own feed entries.
						_ = os.Rename(oldAbs, abs)
					}
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
				// rename(2) silently replaces the target: a file there that is not
				// this node's content is someone's work and is kept aside first.
				if err := e.keepUnsyncedAt(abs, oldAbs, existing.ContentHash, c.ContentHash, localPath); err != nil {
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
