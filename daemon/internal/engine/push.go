package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
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
			out = append(out, LocalChange{Op: "move", RelPath: c.RelPath, OldRelPath: changes[j].RelPath, Hash: c.Hash, Size: c.Size})
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

func (e *Engine) PushLocal(ctx context.Context, sink Sink) error {
	changes, err := e.DetectLocal()
	if err != nil {
		return err
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

	for _, c := range changes {
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
				if err := e.idx.Put(index.Node{NodeID: rn.NodeID, RelPath: c.RelPath, IsDir: true, Version: rn.Version}); err != nil {
					return err
				}
				continue
			}
			fallthrough
		case "update":
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
			if fi, serr := f.Stat(); serr == nil {
				modTime = fi.ModTime()
			}
			rn, conflicted, perr := sink.PushFile(ctx, c.RelPath, base, f, modTime)
			f.Close()
			if perr != nil {
				return perr
			}
			if conflicted {
				// The server version stays as the canonical copy; ours was saved as a
				// conflict copy and will arrive on the next pull (as `name (conflict...).ext`).
				// Leave the index untouched — pull will bring the server's main version and
				// overwrite the local file. No infinite loop as long as the push→pull order is respected.
				continue
			}
			hash := rn.Hash
			if hash == "" {
				hash = c.Hash
			}
			if err := e.idx.Put(index.Node{NodeID: rn.NodeID, RelPath: c.RelPath, Version: rn.Version, ContentHash: hash, Size: c.Size}); err != nil {
				return err
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
						return err
					} else if ok {
						parentID = pn.NodeID
					} else {
						// Parent dir has no indexed node (e.g. implicitly created);
						// EnsureDir is idempotent and returns its id.
						rn, err := sink.EnsureDir(ctx, newParent)
						if err != nil {
							return err
						}
						if err := e.idx.Put(index.Node{NodeID: rn.NodeID, RelPath: newParent, IsDir: true, Version: rn.Version}); err != nil {
							return err
						}
						parentID = rn.NodeID
					}
				}
				if err := mover.MoveNode(ctx, n.NodeID, parentID); err != nil {
					return err
				}
			}
			if path.Base(c.RelPath) != path.Base(c.OldRelPath) {
				if err := mover.RenameNode(ctx, n.NodeID, path.Base(c.RelPath)); err != nil {
					return err
				}
			}
			// Version stays as-is (the server bumps it on move); the next pull's
			// feed entry refreshes it via the hash-match no-op path.
			if err := e.idx.Put(index.Node{NodeID: n.NodeID, RelPath: c.RelPath, Version: n.Version, ContentHash: n.ContentHash, Size: n.Size}); err != nil {
				return err
			}
		case "delete":
			if err := sink.DeleteRemote(ctx, c.RelPath); err != nil {
				return err
			}
			if n, ok := knownNodes[c.RelPath]; ok {
				if err := e.idx.Delete(n.NodeID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func depth(rel string) int { return strings.Count(rel, "/") }
