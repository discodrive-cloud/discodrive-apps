package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"discodrive.org/daemon/internal/index"
)

// RemoteNode is what the server returns after a push (to be recorded in the index).
type RemoteNode struct {
	NodeID  string
	Version int64
	Hash    string
}

// Sink receives local changes (implemented by the HTTP protocol client).
type Sink interface {
	// modTime is the local file's own modification time, so the server can date the
	// content instead of the upload. Zero means "unknown" and leaves it to the server.
	PushFile(ctx context.Context, relPath string, baseVersion *int64, r io.Reader, modTime time.Time) (RemoteNode, bool, error)
	EnsureDir(ctx context.Context, relPath string) (RemoteNode, error)
	DeleteRemote(ctx context.Context, relPath string) error
}

// MoveSink is an optional Sink extension: server-side move/rename by node id
// (PATCH /files/{id}/move and /rename). When the sink implements it, PushLocal
// turns local file moves into these calls instead of delete + re-upload, keeping
// the server node (id, version history) alive.
type MoveSink interface {
	MoveNode(ctx context.Context, nodeID, newParentID string) error
	RenameNode(ctx context.Context, nodeID, newName string) error
}

// pairLocalMoves rewrites a delete of path A plus a create of path B with the same
// content hash into a single move change. Duplicate hashes pair first-come-first-served:
// content is identical, so at worst node identities swap between equal files.
func pairLocalMoves(changes []LocalChange) []LocalChange {
	deletesByHash := map[string][]int{}
	for i, c := range changes {
		if c.Op == "delete" && !c.IsDir && c.Hash != "" {
			deletesByHash[c.Hash] = append(deletesByHash[c.Hash], i)
		}
	}
	movedFrom := map[int]int{} // create index -> consumed delete index
	consumed := map[int]bool{}
	for i, c := range changes {
		if c.Op == "create" && !c.IsDir && c.Hash != "" {
			if q := deletesByHash[c.Hash]; len(q) > 0 {
				movedFrom[i] = q[0]
				consumed[q[0]] = true
				deletesByHash[c.Hash] = q[1:]
			}
		}
	}
	out := make([]LocalChange, 0, len(changes))
	for i, c := range changes {
		if consumed[i] {
			continue // delete absorbed into a move
		}
		if j, ok := movedFrom[i]; ok {
			out = append(out, LocalChange{Op: "move", RelPath: c.RelPath, OldRelPath: changes[j].RelPath, LocalPath: c.LocalPath, HeldSources: uniqueSources(c.HeldSources, []string{c.SourcePath}), Hash: c.Hash, Size: c.Size})
			continue
		}
		out = append(out, c)
	}
	return out
}

// PushLocal detects local changes and uploads them via sink, updating the index.
// bulkDeleteFraction is the share of the index whose disappearance stops a push, and
// bulkDeleteFloor is the number of deletions below which the check does not apply at all.
//
// A sync folder that lost its contents is indistinguishable from one the user emptied on
// purpose: a reinstall that kept the index, a disk that did not mount, a folder moved by hand.
// Pushing that deletes the same files on the server — which is exactly how a re-paired phone,
// sitting on an index that outlived its files, wiped an entire vault. The floor keeps the
// check out of the way of small folders, where clearing three files of three is plainly meant.
const (
	bulkDeleteFraction = 0.20
	bulkDeleteFloor    = 10
)

// BulkDeleteError reports a push stopped because it would have deleted too much. Nothing was
// sent. Call [Engine.ConfirmBulkDelete] to let the next push through if the deletions are real.
type BulkDeleteError struct {
	Deletions int // how many nodes would have been deleted
	Known     int // how many the index held
}

func (e *BulkDeleteError) Error() string {
	return fmt.Sprintf(
		"refusing to delete %d of %d synced items: the local folder looks emptied rather than edited; "+
			"confirm if this is intended", e.Deletions, e.Known)
}

// ConfirmBulkDelete lets the next push carry deletions past the safety threshold. It applies
// once: the confirmation is spent whether or not that push turns out to contain any.
func (e *Engine) ConfirmBulkDelete() { e.bulkDeleteConfirmed = true }

// guardBulkDelete stops a push that would remove a large share of what the index knows,
// unless the user has just confirmed it. Nothing is sent when it refuses.
func (e *Engine) guardBulkDelete(changes []LocalChange) error {
	confirmed := e.bulkDeleteConfirmed
	e.bulkDeleteConfirmed = false

	deletions := 0
	for _, c := range changes {
		if c.Op == "delete" {
			deletions++
		}
	}
	if deletions < bulkDeleteFloor || confirmed {
		return nil
	}
	known, err := e.idx.All()
	if err != nil {
		return err
	}
	if len(known) == 0 || float64(deletions) <= float64(len(known))*bulkDeleteFraction {
		return nil
	}
	return &BulkDeleteError{Deletions: deletions, Known: len(known)}
}

func (e *Engine) PushLocal(ctx context.Context, sink Sink) (result error) {
	e.activityBegin("scan", "")
	defer func() { e.activityEnd(result) }()
	e.pushSeq = 0 // taken afresh by this pass's first putPushed
	// Nothing goes up before the first pull has come down: until then the folder holds
	// the device's previous contents, not the user's new work, and the server is the truth.
	if ready, err := e.idx.MirrorReady(); err != nil || !ready {
		return err
	}
	changes, err := e.DetectLocal()
	if err != nil {
		return err
	}
	// Capture names before move pairing, including inferred source paths. Persist
	// the complete plan before the first request can consume its rename witnesses.
	var pending []index.PendingName
	for _, c := range changes {
		if (c.Op == "create" && (c.LocalPath != "" || c.SourcePath != "" || len(c.HeldSources) > 0)) || (c.Op == "update" && len(c.HeldSources) > 0) {
			local := c.LocalPath
			if local == "" {
				local = c.RelPath
			}
			pending = append(pending, index.PendingName{LocalPath: local, RelPath: c.RelPath, SourcePath: c.SourcePath, IsDir: c.IsDir, Hash: c.Hash, HeldSources: c.HeldSources})
		}
	}
	mover, canMove := sink.(MoveSink)
	if canMove {
		changes = pairLocalMoves(changes)
	}
	// Counted after pairing moves: a file that moved is a delete plus a create, and pairing
	// turns it back into the move it was, so a reorganised folder is not mistaken for a
	// gutted one.
	if err := e.guardBulkDelete(changes); err != nil {
		return err
	}
	if err := e.idx.SetPendingNames(pending); err != nil {
		return err
	}
	// Creates go first (parents before children), then moves (their target parents
	// now exist server-side), deletes last (a moved-out old dir is deleted only
	// after its files left it — the server deletes dirs recursively).
	rank := func(op string) int {
		switch op {
		case "delete":
			return 2
		case "move":
			return 1
		default:
			return 0
		}
	}
	sort.SliceStable(changes, func(i, j int) bool {
		ri, rj := rank(changes[i].Op), rank(changes[j].Op)
		if ri != rj {
			return ri < rj
		}
		di, dj := depth(changes[i].RelPath), depth(changes[j].RelPath)
		if ri == 2 {
			return di > dj // deeper deletes go first
		}
		return di < dj // creates/moves top-down
	})

	knownNodes := map[string]index.Node{}
	if all, err := e.idx.All(); err == nil {
		for _, n := range all {
			knownNodes[n.RelPath] = n
		}
	}

	// A file the server rejects (too large, a name it refuses) is that file's problem: the
	// others still go up, and the caller still pulls. Only a failure that dooms every
	// request — the connection, the credentials, the local index — ends the pass early.
	//
	// A delete never takes server content a failed change still needs (see heldBy).
	var failures []error
	var held []string
	for _, c := range changes {
		if c.Op == "delete" && heldBy(c.RelPath, held) {
			// The next pass sends it, once what it would take has gone up.
			log.Printf("engine: holding the server delete of %s until a failed change it may hold is pushed", c.RelPath)
			continue
		}
		e.activityEnd(nil)
		e.activityBegin("push", c.RelPath)
		err := e.pushOne(ctx, sink, mover, c, knownNodes)
		if err == nil {

			continue
		}
		var stop *stopPush
		if errors.As(err, &stop) {
			return stop.err
		}
		if pushAborts(err) {
			return err
		}
		failures = append(failures, fmt.Errorf("%s: %w", c.RelPath, err))
		held = append(held, stillNeeded(c, changes, err)...)
		held = append(held, c.HeldSources...)
	}
	if len(failures) > 0 {
		return &PushFailures{err: errors.Join(failures...)}
	}
	return nil
}

// stillNeeded is the server content a failed change c leaves where it was, which no delete
// of the same pass may take: deletes come last, and a server folder delete is recursive.
//   - A failed move leaves the node at its old server path.
//   - A failed upload of a new file leaves its source, when it has one, as the only server
//     copy of that content: a file renamed or moved without server-side moves, or moved
//     along with a renamed folder and edited (its hash differs, so it is not paired into a
//     move). The source is found, in this order:
//     1. the deletes of this pass with the same content (not for empty files: they all
//     share one hash);
//     2. the same path under the old name of a renamed folder, as the moves and same-hash
//     pairs of this pass show it (old/sub/g.md → new/sub/g.md: old → new);
//     3. failing that, the deleted files of this pass with the same name — unless the
//     server rejected the upload for good (rejectedForGood): such a file is not waiting
//     to go up, and a same-name guess would hold that delete on every pass.
//     Holding a delete that turns out unrelated only delays it to the next pass.
func stillNeeded(c LocalChange, changes []LocalChange, failure error) []string {
	switch {
	case c.Op == "move":
		return []string{c.OldRelPath}
	case c.Op == "create" && !c.IsDir:
		if c.SourcePath != "" {
			return []string{c.SourcePath}
		}
		var out []string
		if c.Hash != "" && c.Hash != emptyHash {
			for _, d := range changes {
				if d.Op == "delete" && !d.IsDir && d.Hash == c.Hash {
					out = append(out, d.RelPath)
				}
			}
		}
		renamed := false
		for _, p := range renamedFolders(changes) {
			if strings.HasPrefix(c.RelPath, p[1]+"/") {
				out = append(out, p[0]+strings.TrimPrefix(c.RelPath, p[1]))
				renamed = true
			}
		}
		if !renamed && !rejectedForGood(failure) {
			for _, d := range changes {
				if d.Op == "delete" && !d.IsDir && path.Base(d.RelPath) == path.Base(c.RelPath) {
					out = append(out, d.RelPath)
				}
			}
		}
		return out
	}
	return nil
}

// emptyHash is the content hash of an empty file.
var emptyHash = func() string { h := sha256.Sum256(nil); return hex.EncodeToString(h[:]) }()

// renamedFolders lists the (old, new) server folder pairs the moves of a pass show: a
// node moved from old/a/x to new/a/x means old/a became new/a and old became new. Without
// server-side moves, a create and a delete with the same content and name are the move.
func renamedFolders(changes []LocalChange) [][2]string {
	var out [][2]string
	add := func(from, to string) {
		f, t := strings.Split(from, "/"), strings.Split(to, "/")
		for len(f) > 1 && len(t) > 1 && f[len(f)-1] == t[len(t)-1] {
			f, t = f[:len(f)-1], t[:len(t)-1]
			out = append(out, [2]string{strings.Join(f, "/"), strings.Join(t, "/")})
		}
	}
	for _, c := range changes {
		switch {
		case c.Op == "move":
			add(c.OldRelPath, c.RelPath)
		case c.Op == "create" && !c.IsDir && c.Hash != "" && c.Hash != emptyHash:
			for _, d := range changes {
				if d.Op == "delete" && !d.IsDir && d.Hash == c.Hash && path.Base(d.RelPath) == path.Base(c.RelPath) {
					add(d.RelPath, c.RelPath)
				}
			}
		}
	}
	return out
}

// rejectedForGood reports whether err is the server refusing a request in a way a retry
// will not change: a 4xx other than 408 (timeout), 409 (conflict), 423 (locked), 425 (too
// early) and 429 (too many requests). The status comes from the error (HTTPStatus, as
// protocol.StatusError has it) or, failing that, from its text ("PUT …: 413: …").
//
// A chunked upload's later steps (/upload chunk N, /upload complete, /upload status) fail
// about the upload session, not the file — 404 "upload session not found", 400 "chunk
// exceeds the declared file size" or "invalid chunk number", 409 "chunk out of order" —
// and the next pass opens a new session: never a permanent rejection. Only the session's
// start (/upload/init: too large, a refused name) or a plain PUT can refuse the file.
func rejectedForGood(err error) bool {
	if err == nil {
		return false
	}
	if uploadSessionStep.MatchString(err.Error()) {
		return false
	}
	code := 0
	var hs interface{ HTTPStatus() int }
	if errors.As(err, &hs) {
		code = hs.HTTPStatus()
	} else if m := statusInText.FindStringSubmatch(err.Error()); m != nil {
		code, _ = strconv.Atoi(m[2])
	}
	switch code {
	case 408, 409, 423, 425, 429:
		return false
	}
	return code >= 400 && code < 500
}

var uploadSessionStep = regexp.MustCompile(`(^|: )/upload (chunk \d+|complete|status): `)

var statusInText = regexp.MustCompile(`(^|: )(4\d\d)(:| |$)`)

// heldBy reports whether deleting server path rel would take one of the held paths: rel
// itself, or a folder above it.
func heldBy(rel string, held []string) bool {
	for _, h := range held {
		if h == rel || strings.HasPrefix(h, rel+"/") {
			return true
		}
	}
	return false
}

// pushOne sends one local change. Index failures come back as *stopPush: the local
// database failing is not about this file.
func (e *Engine) pushOne(ctx context.Context, sink Sink, mover MoveSink, c LocalChange, knownNodes map[string]index.Node) error {
	abs, err := e.abs(c.RelPath)
	if err != nil {
		return err
	}
	switch c.Op {
	case "create":
		if c.IsDir {
			rn, err := sink.EnsureDir(ctx, c.RelPath)
			if err != nil {
				return err
			}
			local := c.LocalPath
			if local == "" {
				local = c.RelPath
			}
			if err := e.putPushedDir(rn, c.RelPath, local); err != nil {
				return &stopPush{err}
			}
			return nil
		}
		fallthrough
	case "update":
		if c.LocalPath != "" && c.LocalPath != c.RelPath {
			if abs, err = e.abs(c.LocalPath); err != nil {
				return err
			}
		}
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		var base *int64
		if n, ok := knownNodes[c.RelPath]; ok {
			v := n.Version
			base = &v
		}
		// Stat the open handle rather than the path: same file we are about to send,
		// and no second lookup that could race a concurrent local edit.
		var modTime time.Time
		openInfo := statOrNil(f)
		if openInfo != nil {
			modTime = openInfo.ModTime()
		}
		rn, conflicted, perr := sink.PushFile(ctx, c.RelPath, base, f, modTime)
		// A file rewritten while it was being sent may have reached the server torn.
		// The handle is the file on disk (not a buffered copy, which chunked uploads
		// need), so compare it before and after: if it moved, leave the index dirty
		// and the next pass sends the file again.
		changedDuringUpload := false
		if before, after := openInfo, statOrNil(f); before == nil || after == nil ||
			before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			changedDuringUpload = true
		}
		f.Close()
		if perr != nil {
			return perr
		}
		if conflicted {
			// The server version stays as the canonical copy; ours was saved as a
			// conflict copy and will arrive on the next pull (as `name (conflict...).ext`).
			// Leave the index untouched — pull will bring the server's main version and
			// overwrite the local file, which it may: this exact content is already safe
			// on the server. No infinite loop as long as the push→pull order is respected.
			e.markSavedAsConflict(c.LocalPath, c.RelPath, c.Hash)
			local := c.LocalPath
			if local == "" {
				local = c.RelPath
			}
			if err := e.idx.CompletePendingName(local); err != nil {
				return &stopPush{err}
			}
			return nil
		}
		hash := rn.Hash
		if hash == "" {
			hash = c.Hash
		}
		if changedDuringUpload {
			hash = "" // never matches a real hash: DetectLocal reports it as changed
		}
		if err := e.putPushed(index.Node{NodeID: rn.NodeID, RelPath: c.RelPath, LocalPath: c.LocalPath, Version: rn.Version, ContentHash: hash, Size: c.Size}); err != nil {
			return &stopPush{err}
		}
	case "move":
		// Produced only when the sink implements MoveSink (see pairing above).
		n, known := knownNodes[c.OldRelPath]
		if !known {
			// Deletes are generated from the same index snapshot, so this only
			// happens if the index read above failed; retry next cycle.
			return fmt.Errorf("move source %s not in index", c.OldRelPath)
		}
		newParent, oldParent := path.Dir(c.RelPath), path.Dir(c.OldRelPath)
		if newParent != oldParent {
			parentID := ""
			if newParent != "." {
				if pn, ok, err := e.idx.GetByPath(newParent); err != nil {
					return &stopPush{err}
				} else if ok {
					parentID = pn.NodeID
				} else {
					// Parent dir has no indexed node (e.g. implicitly created);
					// EnsureDir is idempotent and returns its id.
					rn, err := sink.EnsureDir(ctx, newParent)
					if err != nil {
						return err
					}
					if err := e.putPushedDir(rn, newParent, newParent); err != nil {
						return &stopPush{err}
					}
					parentID = rn.NodeID
				}
			}
			if err := mover.MoveNode(ctx, n.NodeID, parentID); err != nil {
				return e.pushGoneAsNew(ctx, sink, mover, c, n, knownNodes, err)
			}
		}
		if path.Base(c.RelPath) != path.Base(c.OldRelPath) {
			if err := mover.RenameNode(ctx, n.NodeID, path.Base(c.RelPath)); err != nil {
				return e.pushGoneAsNew(ctx, sink, mover, c, n, knownNodes, err)
			}
		}
		// Version stays as-is (the server bumps it on move); the next pull's
		// feed entry refreshes it via the hash-match no-op path.
		if err := e.putPushed(index.Node{NodeID: n.NodeID, RelPath: c.RelPath, LocalPath: c.LocalPath, Version: n.Version, ContentHash: n.ContentHash, Size: n.Size}); err != nil {
			return &stopPush{err}
		}
	case "delete":
		if n, ok := knownNodes[c.RelPath]; ok {
			if lives, err := e.relocatedThisPass(n); err != nil {
				return &stopPush{err}
			} else if lives {
				return nil
			}
		}
		if err := sink.DeleteRemote(ctx, c.RelPath); err != nil {
			return err
		}
		if n, ok := knownNodes[c.RelPath]; ok {
			if err := e.idx.Delete(n.NodeID); err != nil {
				return &stopPush{err}
			}
		}
	}
	return nil
}

// putPushed records a node a push just wrote on the server. The server placed it after
// everything the feed has delivered so far, so of two rows at one path it is the live one
// (see removeDeleted): its seq is one past both the cursor and every seq the index holds.
// The pull applies changes past one that failed while the cursor stays behind it, so rows
// can carry seqs above the cursor. Its feed echo records the real seq.
//
// The seq is taken once per push pass: rows pushed together are not placed after one
// another, and a row the feed never echoes does not push the next pass's seq further.
func (e *Engine) putPushed(n index.Node) error {
	if e.pushSeq == 0 {
		cursor, err := e.idx.Cursor()
		if err != nil {
			return err
		}
		max, err := e.idx.MaxSeq()
		if err != nil {
			return err
		}
		e.pushSeq = maxInt64(cursor, max) + 1
	}
	return e.idx.RecordPush(n, e.pushSeq)
}

// putPushedDir records the folder the server answered EnsureDir with, at server path rel
// and local path local (they differ inside a folder shown under another name, see
// DetectLocal). EnsureDir is
// idempotent: it answers with the folder the server already has at that path, which the
// index may have at another local path.
//   - That local path is gone from disk: the user renamed the folder onto its server path
//     (typically a generated name, see localPathFor, back to the plain one). The row
//     follows it, and the delete the scan reports for the old path is dropped (see
//     relocatedThisPass): it would delete this very folder on the server.
//   - That local path is still there: the folder pushed is another one left without a row
//     (a ghost's, see removeDeleted), and the row stays where it is. Re-pointing it would
//     strand its children's rows under the old name and make the next push upload that
//     name as a new folder.
func (e *Engine) putPushedDir(rn RemoteNode, rel, local string) error {
	if have, ok, err := e.idx.Get(rn.NodeID); err != nil {
		return err
	} else if ok && have.LocalPath != local {
		if p, err := e.abs(have.LocalPath); err != nil || existsOnDisk(p) {
			return e.idx.CompletePendingName(local)
		}
	}
	return e.putPushed(index.Node{NodeID: rn.NodeID, RelPath: rel, LocalPath: local, IsDir: true, Version: rn.Version})
}

// relocatedThisPass reports whether node n, which the scan found gone from its local path,
// was recorded at another local path, one that exists, earlier in this push pass: the
// server answered a create or an upload there with this node's id (a local rename onto the
// node's own server path). The node lives on, and deleting its server path would delete it.
func (e *Engine) relocatedThisPass(n index.Node) (bool, error) {
	cur, ok, err := e.idx.Get(n.NodeID)
	if err != nil || !ok || cur.LocalPath == n.LocalPath {
		return false, err
	}
	p, err := e.abs(cur.LocalPath)
	return err == nil && existsOnDisk(p), nil
}

func existsOnDisk(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// pushGoneAsNew handles a move or rename the server refused because the node no longer
// exists there (hard-deleted while its delete event never arrived). The node cannot be
// moved, so its stale index entry is dropped and the file at its new path is pushed as a
// new one — the local copy is kept and the server gets it back. Any other error is
// returned as it is.
func (e *Engine) pushGoneAsNew(ctx context.Context, sink Sink, mover MoveSink, c LocalChange, n index.Node, knownNodes map[string]index.Node, err error) error {
	if !errors.Is(err, ErrNodeNotFound) {
		return err
	}
	if derr := e.idx.Delete(n.NodeID); derr != nil {
		return &stopPush{derr}
	}
	delete(knownNodes, c.OldRelPath)
	c.Op = "create"
	return e.pushOne(ctx, sink, mover, c, knownNodes)
}

// stopPush marks a failure that ends the whole pass rather than one file's push.
type stopPush struct{ err error }

func (s *stopPush) Error() string { return s.err.Error() }
func (s *stopPush) Unwrap() error { return s.err }

// authFailure matches the status of a rejected credential in a protocol error message
// ("PUT /sync/file: 401 Unauthorized", "device token exchange: 403", a StatusError's
// "/upload/init: 401: <body>"). The protocol package imports this one, and its
// StatusError exposes the code only as a field, so it cannot be matched by type here.
var authFailure = regexp.MustCompile(`(^|: )(401|403)(:| |$)`)

// pushAborts reports whether err means no other file can be pushed right now either:
// the pass was cancelled, the server could not be reached, or the device is no longer
// authorised. Anything else is about the one file and the push goes on.
func pushAborts(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	return authFailure.MatchString(msg) || strings.Contains(msg, "authorization failed")
}

func statOrNil(f *os.File) os.FileInfo {
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	return fi
}

func depth(rel string) int { return strings.Count(rel, "/") }

// PushFailures reports files a push could not send while it went on with the others:
// a rejected upload (too large, a name the server refuses) is about that one file, and
// must not hold back the rest of the pass or the pull behind it. Transport and auth
// failures are returned as they are instead — they stop the pass.
type PushFailures struct{ err error }

func (p *PushFailures) Error() string { return p.err.Error() }
func (p *PushFailures) Unwrap() error { return p.err }

// OnlyPushFailures reports whether a pass error is nothing but files the server rejected:
// everything else was pushed and the pull succeeded. Such a pass finished; hosts report
// the rejection without treating the client as offline or backing off. A joined error
// (a failed pull as well) does not qualify.
func OnlyPushFailures(err error) bool {
	_, ok := err.(*PushFailures)
	return ok
}
