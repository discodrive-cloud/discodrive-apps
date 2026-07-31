package desktop

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/safepath"
)

// ServerAPI is the subset of *protocol.Client the controller depends on, narrowed
// to an interface so tests can substitute a fake. *protocol.Client satisfies it.
type ServerAPI interface {
	Changes(ctx context.Context, since int64, limit int) ([]engine.Change, int64, bool, error)
	Download(ctx context.Context, nodeID string, w io.Writer) error
	CreateFolder(ctx context.Context, parentID, name string) error
	RenameNode(ctx context.Context, nodeID, newName string) error
	MoveNode(ctx context.Context, nodeID, newParentID string) error
	DeleteNode(ctx context.Context, nodeID string) error
	UploadFile(ctx context.Context, parentID, name string, r io.Reader, modTime time.Time) error
	EnsureDir(ctx context.Context, relPath string) (engine.RemoteNode, error)
	PushFile(ctx context.Context, relPath string, baseVersion *int64, r io.Reader, modTime time.Time) (engine.RemoteNode, bool, error)
}

// Entry is one row in a directory listing: the indexed node plus its local state.
type Entry struct {
	Node      index.Node
	State     string // "" (no local copy) | "cached" | "pinned"
	Stale     bool   // local copy older than the server version
	LocalPath string // path to the cached copy, "" if none
}

// Controller wires the server, the local index, and the content cache into the
// on-demand operations the UI calls.
type Controller struct {
	srv        ServerAPI
	idx        *index.Index
	contentDir string

	mu       sync.Mutex
	sessions map[string]*vaultSession // keyed by vault server relPath
}

// NewController builds a controller. contentDir must already exist (or be creatable
// by the caller before Open/Pin are used).
func NewController(srv ServerAPI, idx *index.Index, contentDir string) *Controller {
	return &Controller{
		srv:        srv,
		idx:        idx,
		contentDir: contentDir,
		sessions:   make(map[string]*vaultSession),
	}
}

// changesPageLimit caps how many changes are pulled per request during Refresh.
const changesPageLimit = 500

// Refresh pulls the change delta from the current cursor and applies it to the
// local index without downloading any content. Returns the number of changes applied.
func (c *Controller) Refresh(ctx context.Context) (int, error) {
	since, err := c.idx.Cursor()
	if err != nil {
		return 0, err
	}
	applied := 0
	for {
		changes, next, hasMore, err := c.srv.Changes(ctx, since, changesPageLimit)
		if err != nil {
			return applied, err
		}
		for _, ch := range changes {
			if ch.Deleted || ch.Op == "delete" {
				// Remove the cached file from disk too, not just the index record.
				_ = c.RemoveLocal(ch.NodeID)
				if err := c.idx.Delete(ch.NodeID); err != nil {
					return applied, err
				}
			} else {
				// On a rename/move (rel_path changed) relocate the cached file so the
				// old name does not orphan on disk.
				if old, ok, _ := c.idx.Get(ch.NodeID); ok && old.RelPath != ch.RelPath {
					c.reconcileLocalRelPath(ch.NodeID, ch.RelPath)
				}
				if err := c.idx.Put(index.Node{
					NodeID:      ch.NodeID,
					RelPath:     ch.RelPath,
					IsDir:       ch.IsDir,
					Version:     ch.Version,
					ContentHash: ch.ContentHash,
					Size:        ch.Size,
				}); err != nil {
					return applied, err
				}
			}
			applied++
		}
		advanced := next > since
		if advanced {
			since = next
			if err := c.idx.SetCursor(next); err != nil {
				return applied, err
			}
		}
		// Stop when the server says there is no more, or when the cursor failed to
		// advance — the latter guards against an infinite loop if a misbehaving
		// server returns hasMore=true without moving the cursor forward.
		if !hasMore || !advanced {
			return applied, nil
		}
	}
}

// List returns the children of the directory at relPath (root is ""), each annotated
// with its local cache state. The server version drives the stale flag.
func (c *Controller) List(relPath string) ([]Entry, error) {
	nodes, err := c.idx.Children(relPath)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(nodes))
	for _, n := range nodes {
		state, stale, path := c.idx.LocalStatus(n.NodeID, n.Version)
		entries = append(entries, Entry{Node: n, State: state, Stale: stale, LocalPath: path})
	}
	return entries, nil
}

// fetch downloads nodeID's content into contentDir and marks it with state.
// Returns the local path. Reused by Open (state "cached") and Pin (state "pinned").
func (c *Controller) fetch(ctx context.Context, nodeID, state string) (string, error) {
	n, ok, err := c.idx.Get(nodeID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("desktop: unknown node %q", nodeID)
	}
	// Cache content under its real relative path so the local folder mirrors the
	// server tree with human-readable names (not opaque node IDs). RelPath is
	// server-controlled, so contain it to contentDir (reject ../ traversal + symlinks).
	dest, err := safepath.Join(c.contentDir, n.RelPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if err := c.srv.Download(ctx, nodeID, f); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := c.idx.SetLocal(nodeID, state, n.Version, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Open returns a local path to nodeID's content, downloading and caching it if the
// local copy is missing or stale.
func (c *Controller) Open(ctx context.Context, nodeID string) (string, error) {
	n, ok, err := c.idx.Get(nodeID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("desktop: unknown node %q", nodeID)
	}
	state, stale, path := c.idx.LocalStatus(nodeID, n.Version)
	if state != "" && !stale {
		return path, nil
	}
	return c.fetch(ctx, nodeID, "cached")
}

// Pin marks nodeID as pinned so it is kept locally. If a fresh local copy already
// exists it is just promoted to "pinned" (no re-download); otherwise the content is
// downloaded first.
func (c *Controller) Pin(ctx context.Context, nodeID string) (string, error) {
	n, ok, err := c.idx.Get(nodeID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("desktop: unknown node %q", nodeID)
	}
	if state, stale, path := c.idx.LocalStatus(nodeID, n.Version); state != "" && !stale {
		if err := c.idx.SetLocal(nodeID, "pinned", n.Version, path); err != nil {
			return "", err
		}
		return path, nil
	}
	return c.fetch(ctx, nodeID, "pinned")
}

// Unpin demotes a pinned node back to cached; the content stays on disk.
func (c *Controller) Unpin(nodeID string) error {
	n, ok, err := c.idx.Get(nodeID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("desktop: unknown node %q", nodeID)
	}
	return c.idx.SetLocal(nodeID, "cached", n.Version, c.idx.LocalPathOf(nodeID))
}

// RemoveLocal deletes the cached file and clears the local record (state → "").
func (c *Controller) RemoveLocal(nodeID string) error {
	if path := c.idx.LocalPathOf(nodeID); path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return c.idx.DeleteLocal(nodeID)
}

// reconcileLocalRelPath moves a node's cached file to mirror its new server rel_path
// after a rename/move, so the old name does not orphan on disk. If the move fails it
// purges the local copy so the next Open re-downloads cleanly. No-op if nothing is
// cached locally.
func (c *Controller) reconcileLocalRelPath(nodeID, newRelPath string) {
	oldPath := c.idx.LocalPathOf(nodeID)
	if oldPath == "" {
		return
	}
	newPath, err := safepath.Join(c.contentDir, newRelPath)
	if err != nil {
		_ = c.RemoveLocal(nodeID)
		return
	}
	if oldPath == newPath {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		_ = c.RemoveLocal(nodeID)
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		_ = c.RemoveLocal(nodeID)
		return
	}
	_ = c.idx.RelocateLocal(nodeID, newPath)
}

// CreateFolder creates a directory on the server under parentID.
func (c *Controller) CreateFolder(ctx context.Context, parentID, name string) error {
	return c.srv.CreateFolder(ctx, parentID, name)
}

// Rename renames a node on the server.
func (c *Controller) Rename(ctx context.Context, nodeID, newName string) error {
	return c.srv.RenameNode(ctx, nodeID, newName)
}

// Move reparents a node on the server.
func (c *Controller) Move(ctx context.Context, nodeID, newParentID string) error {
	return c.srv.MoveNode(ctx, nodeID, newParentID)
}

// Delete removes a node on the server.
func (c *Controller) Delete(ctx context.Context, nodeID string) error {
	return c.srv.DeleteNode(ctx, nodeID)
}

// Upload streams a new file into parentID on the server. modTime is the content's own
// date; pass the zero time when the caller has none and the server should date it.
func (c *Controller) Upload(ctx context.Context, parentID, name string, r io.Reader, modTime time.Time) error {
	return c.srv.UploadFile(ctx, parentID, name, r, modTime)
}
