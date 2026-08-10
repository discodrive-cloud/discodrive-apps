package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/safepath"
)

// Browser is a bindable, index-based (offline) file browser over the whole vault. Lists come
// from a local index built from /sync/changes; files are downloaded on demand into rootDir.
type Browser struct {
	client  *protocol.Client
	idx     *index.Index
	rootDir string
	// chunkSize is the chunked-upload chunk length; tests shrink it to exercise
	// multi-chunk paths without writing megabytes.
	chunkSize int64
}

type browseEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
	Version   int64  `json:"version"`
	Cached    bool   `json:"cached"`
	Pinned    bool   `json:"pinned"`
	Stale     bool   `json:"stale"`
	LocalPath string `json:"localPath"`
}

// NewBrowser builds a browser. rootDir — folder for downloaded files (the UI passes a shared
// path); indexDBPath — sqlite index (app-private). insecure — accept self-signed TLS.
func NewBrowser(serverURL, deviceToken, rootDir, indexDBPath string, insecure bool) (*Browser, error) {
	setInsecure(insecure)
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, err
	}
	idx, err := index.Open(indexDBPath)
	if err != nil {
		return nil, err
	}
	// An index built against another server lists files this one does not have; showing them
	// would be a lie, and every open of one would 404.
	if stored, serr := idx.ServerURL(); serr == nil && stored != "" && stored != serverURL {
		if err := idx.Clear(); err != nil {
			idx.Close()
			return nil, err
		}
	}
	if err := idx.SetServerURL(serverURL); err != nil {
		idx.Close()
		return nil, err
	}
	return &Browser{client: protocol.NewUnscoped(serverURL, deviceToken), idx: idx, rootDir: rootDir,
		chunkSize: defaultChunkSize}, nil
}

// Refresh pulls all change-feed metadata into the index (no file content downloaded).
func (b *Browser) Refresh() error {
	return pullChanges(context.Background(), b.client, b.idx)
}

// pullChanges pulls all change-feed metadata into idx (no file content). Shared by the
// Browser and the Vault facade.
func pullChanges(ctx context.Context, client *protocol.Client, idx *index.Index) error {
	since, err := idx.Cursor()
	if err != nil {
		return err
	}
	for {
		changes, cursor, hasMore, err := client.Changes(ctx, since, 500)
		if err != nil {
			return err
		}
		// One transaction per page, not per row: a row at a time meant a commit — on a
		// phone, a fsync — apiece, and the first pull after pairing spent minutes on that
		// while the file list sat empty.
		if err := idx.Batch(func(b *index.Batch) error {
			for _, c := range changes {
				if c.Deleted {
					if err := b.Delete(c.NodeID); err != nil {
						return err
					}
				} else if err := b.Put(index.Node{
					NodeID: c.NodeID, RelPath: c.RelPath, IsDir: c.IsDir,
					Version: c.Version, ContentHash: c.ContentHash, Size: c.Size,
				}); err != nil {
					return err
				}
			}
			// Once, at the end: the page is all-or-nothing, so the cursor either covers
			// every row in it or none of them.
			if n := len(changes); n > 0 {
				return b.SetCursor(changes[n-1].Seq)
			}
			return nil
		}); err != nil {
			return err
		}
		since = cursor
		if !hasMore {
			return nil
		}
	}
}

// List returns the children of parentNodeID ("" = root) as a JSON array of browseEntry.
func (b *Browser) List(parentNodeID string) (string, error) {
	parentPath := ""
	if parentNodeID != "" {
		n, ok, err := b.idx.Get(parentNodeID)
		if err != nil {
			return "", err
		}
		if ok {
			parentPath = n.RelPath
		}
	}
	kids, err := b.idx.Children(parentPath)
	if err != nil {
		return "", err
	}
	out := make([]browseEntry, 0, len(kids))
	for _, n := range kids {
		state, stale, lp := b.idx.LocalStatus(n.NodeID, n.Version)
		out = append(out, browseEntry{
			ID: n.NodeID, Name: path.Base(n.RelPath), IsDir: n.IsDir, Size: n.Size, Version: n.Version,
			Cached: state != "", Pinned: state == "pinned", Stale: stale, LocalPath: lp,
		})
	}
	js, err := json.Marshal(out)
	return string(js), err
}

// Download fetches the file content into rootDir/<rel_path> and records it as cached.
func (b *Browser) Download(nodeID string) (string, error) {
	return b.download(nodeID, "cached")
}

func (b *Browser) download(nodeID, state string) (string, error) {
	n, ok, err := b.idx.Get(nodeID)
	if err != nil || !ok {
		return "", err
	}
	// RelPath is server-controlled; contain it to rootDir so a malicious server can't
	// write outside the download folder via ../ traversal or a symlinked component.
	// Localize first: a name holding characters the filesystem rejects (a note titled
	// "…worth it?.md") cannot be created at all on Android's storage.
	dst, err := safepath.Join(b.rootDir, localname.Localize(n.RelPath))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	if derr := b.client.Download(context.Background(), nodeID, f); derr != nil {
		f.Close()
		return "", derr
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := b.idx.SetLocal(nodeID, state, n.Version, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// Pin downloads (if needed) and marks the file pinned.
func (b *Browser) Pin(nodeID string) error {
	_, err := b.download(nodeID, "pinned")
	return err
}

// Unpin keeps the local copy but clears the pinned flag.
func (b *Browser) Unpin(nodeID string) error {
	n, ok, err := b.idx.Get(nodeID)
	if err != nil || !ok {
		return err
	}
	return b.idx.SetLocal(nodeID, "cached", n.Version, b.idx.LocalPathOf(nodeID))
}

// RemoveLocal deletes the local copy and the cache record.
func (b *Browser) RemoveLocal(nodeID string) error {
	if p := b.idx.LocalPathOf(nodeID); p != "" {
		_ = os.Remove(p)
	}
	return b.idx.DeleteLocal(nodeID)
}

// LocalPath returns the cached file path or "".
func (b *Browser) LocalPath(nodeID string) string { return b.idx.LocalPathOf(nodeID) }

// RelPath returns the rel_path of nodeID, or "" if unknown. Used by the UI to derive the
// vaultRoot of the folder being unlocked.
func (b *Browser) RelPath(nodeID string) string {
	n, ok, err := b.idx.Get(nodeID)
	if err != nil || !ok {
		return ""
	}
	return n.RelPath
}

// Existence answers from ExistsWithHash. Kept as constants so the Kotlin/Swift side can
// compare against a documented set rather than bare literals.
const (
	ExistsAbsent    = "absent"    // nothing lives at that name
	ExistsSame      = "same"      // a file with the same content is already there
	ExistsDifferent = "different" // the name is taken by other content (or by a folder)
)

// ExistsWithHash reports what sits at name inside parentNodeID ("" = root), comparing the
// server's content hash against sha. It reads the local index only — no network — so a
// caller uploading a batch pays one Refresh, not one round trip per file.
//
// Only a proven match reports ExistsSame: an unknown hash on either side reports
// ExistsDifferent, because the server treats a same-named upload as a new version of the
// existing node. Guessing "same" would silently skip a file; guessing "different" only
// costs a suffixed copy.
func (b *Browser) ExistsWithHash(parentNodeID, name, sha string) (string, error) {
	parentPath := ""
	if parentNodeID != "" {
		n, ok, err := b.idx.Get(parentNodeID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("unknown parent node %q", parentNodeID)
		}
		parentPath = n.RelPath
	}
	rel := name
	if parentPath != "" {
		rel = parentPath + "/" + name
	}
	n, ok, err := b.idx.GetByPath(rel)
	if err != nil {
		return "", err
	}
	switch {
	case !ok:
		return ExistsAbsent, nil
	case n.IsDir:
		return ExistsDifferent, nil
	case sha != "" && n.ContentHash == sha:
		return ExistsSame, nil
	default:
		return ExistsDifferent, nil
	}
}

// Mkdir creates a folder under parentNodeID ("" = root), then refreshes the index.
func (b *Browser) Mkdir(parentNodeID, name string) error {
	if err := b.client.CreateFolder(context.Background(), parentNodeID, name); err != nil {
		return err
	}
	return b.Refresh()
}

// Upload pushes a local file into parentNodeID ("" = root), then refreshes the index.
func (b *Browser) Upload(localPath, parentNodeID string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Carry the file's own date so a photo from 2019 does not land dated today.
	var modTime time.Time
	if fi, serr := f.Stat(); serr == nil {
		modTime = fi.ModTime()
	}
	if err := b.client.UploadFile(context.Background(), parentNodeID, filepath.Base(localPath), f, modTime); err != nil {
		return err
	}
	return b.Refresh()
}

// UploadAs uploads localPath into parentNodeID ("" = root) under an explicit server-side
// name, using the resumable chunked protocol — a dropped connection continues from the
// server's next_chunk instead of restarting a multi-gigabyte video.
//
// Unlike Upload it does NOT refresh the index: a batch (auto-upload sending a hundred
// photos) pays one Refresh at the end instead of one change-feed pass per file.
//
// A rejected size surfaces as ErrUploadSizeMismatch — the file changed while it was being
// sent, so the caller should re-stat and start over rather than retry the same session.
func (b *Browser) UploadAs(localPath, parentNodeID, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), uploadDeadline)
	defer cancel()
	return uploadFile(ctx, b.client, localPath, parentNodeID, name, b.chunkSize)
}

// EnsureFolder returns the node id of the child folder called name under parentNodeID
// ("" = root), creating it when it is not there yet. Idempotent: an existing folder is
// answered straight from the index, without a round trip.
func (b *Browser) EnsureFolder(parentNodeID, name string) (string, error) {
	parentPath := ""
	if parentNodeID != "" {
		n, ok, err := b.idx.Get(parentNodeID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("unknown parent node %q", parentNodeID)
		}
		parentPath = n.RelPath
	}
	rel := name
	if parentPath != "" {
		rel = parentPath + "/" + name
	}
	if n, ok, err := b.idx.GetByPath(rel); err != nil {
		return "", err
	} else if ok {
		if !n.IsDir {
			return "", fmt.Errorf("%q is a file, not a folder", rel)
		}
		return n.NodeID, nil
	}
	if err := b.client.CreateFolder(context.Background(), parentNodeID, name); err != nil {
		return "", err
	}
	if err := b.Refresh(); err != nil {
		return "", err
	}
	n, ok, err := b.idx.GetByPath(rel)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("created %q but it is missing from the change feed", rel)
	}
	return n.NodeID, nil
}

// Rename renames a node, then refreshes.
func (b *Browser) Rename(nodeID, newName string) error {
	if err := b.client.RenameNode(context.Background(), nodeID, newName); err != nil {
		return err
	}
	return b.Refresh()
}

// Move moves a node under newParentNodeID ("" = root), then refreshes.
func (b *Browser) Move(nodeID, newParentNodeID string) error {
	if err := b.client.MoveNode(context.Background(), nodeID, newParentNodeID); err != nil {
		return err
	}
	return b.Refresh()
}

// Delete soft-deletes a node, then refreshes.
func (b *Browser) Delete(nodeID string) error {
	if err := b.client.DeleteNode(context.Background(), nodeID); err != nil {
		return err
	}
	return b.Refresh()
}

// Close releases the index.
func (b *Browser) Close() error { return b.idx.Close() }
