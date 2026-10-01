package desktop

import (
	"context"
	"errors"
	"os"
	"testing"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
)

// A node the server hard-deleted while its delete event was lost stays in the index as a
// ghost. The first request about it answers 404 not-found, and the controller then applies
// the delete it missed: the node and its subtree leave the index and the content cache.

func ghostController(t *testing.T) (*Controller, *idxHandle, *fakeServer) {
	t.Helper()
	f := &fakeServer{
		pages: []fakePage{{changes: []engine.Change{
			{Seq: 1, Op: "upsert", NodeID: "new1", RelPath: "new1", IsDir: true, Version: 1},
			{Seq: 2, Op: "upsert", NodeID: "app", RelPath: "new1/DiscoDrive.app", IsDir: true, Version: 1},
			{Seq: 3, Op: "upsert", NodeID: "plist", RelPath: "new1/DiscoDrive.app/Info.plist", Version: 1, Size: 5},
			{Seq: 4, Op: "upsert", NodeID: "keep", RelPath: "new1/keep.txt", Version: 1, Size: 4},
		}, next: 4}},
		download: map[string][]byte{"plist": []byte("plist"), "keep": []byte("keep")},
	}
	c, h := newTestController(t, f)
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return c, h, f
}

func assertGone(t *testing.T, idx *index.Index, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, ok, _ := idx.Get(id); ok {
			t.Errorf("%s still in the index", id)
		}
	}
}

func assertKept(t *testing.T, idx *index.Index, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, ok, _ := idx.Get(id); !ok {
			t.Errorf("%s dropped from the index, want it kept", id)
		}
	}
}

func TestDeleteOfGhostSucceedsAndForgetsSubtree(t *testing.T) {
	c, h, f := ghostController(t)
	// A cached copy under the ghost goes the way a server delete takes it.
	cached, err := c.Pin(context.Background(), "plist")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	f.gone = map[string]bool{"app": true, "plist": true}

	if err := c.Delete(context.Background(), "app"); err != nil {
		t.Fatalf("Delete of a ghost: %v, want success", err)
	}
	assertGone(t, h.idx, "app", "plist")
	assertKept(t, h.idx, "new1", "keep")
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Errorf("cached copy of the ghost still on disk: %v", err)
	}
}

func TestRenameOfGhostReportsGoneAndForgets(t *testing.T) {
	c, h, f := ghostController(t)
	f.gone = map[string]bool{"app": true, "plist": true}

	err := c.Rename(context.Background(), "app", "x.app")
	if !errors.Is(err, ErrNodeGone) {
		t.Fatalf("Rename of a ghost: %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "app", "plist")
	assertKept(t, h.idx, "new1", "keep")
}

func TestOpenOfGhostReportsGoneAndLeavesNoFile(t *testing.T) {
	c, h, f := ghostController(t)
	f.gone = map[string]bool{"plist": true}

	_, err := c.Open(context.Background(), "plist")
	if !errors.Is(err, ErrNodeGone) {
		t.Fatalf("Open of a ghost: %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "plist")
	assertKept(t, h.idx, "app", "new1", "keep")
	if entries, _ := os.ReadDir(c.contentDir + "/new1/DiscoDrive.app"); len(entries) != 0 {
		t.Errorf("download of a ghost left %d file(s) in the cache", len(entries))
	}
}

// A move names two nodes, and the server answers the same 404 for either one missing.
// The controller asks which one it was and forgets only that one.
func TestMoveIntoGhostFolderForgetsTheFolderNotTheNode(t *testing.T) {
	c, h, f := ghostController(t)
	f.gone = map[string]bool{"app": true, "plist": true}

	err := c.Move(context.Background(), "keep", "app")
	if !errors.Is(err, ErrNodeGone) {
		t.Fatalf("Move into a ghost: %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "app", "plist")
	assertKept(t, h.idx, "keep", "new1")
}

func TestMoveOfGhostForgetsTheNode(t *testing.T) {
	c, h, f := ghostController(t)
	f.gone = map[string]bool{"keep": true}

	if err := c.Move(context.Background(), "keep", "app"); !errors.Is(err, ErrNodeGone) {
		t.Fatalf("Move of a ghost: %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "keep")
	assertKept(t, h.idx, "app", "plist", "new1")
}

// Other failures leave the index alone: only the server saying "not found" about the
// node makes a client forget it.
func TestOtherErrorsDoNotForget(t *testing.T) {
	c, h, f := ghostController(t)
	f.failWith = errors.New("DELETE /files: 500")
	if err := c.Delete(context.Background(), "app"); err == nil {
		t.Fatal("Delete: want the 500 surfaced")
	}
	assertKept(t, h.idx, "app", "plist")
}

// The server allows "Docs" and "docs" as siblings. Forgetting a ghost "Docs" must leave the
// live "docs" tree alone — its rows and its cached files.
func TestForgettingAGhostKeepsASiblingThatDiffersOnlyInCase(t *testing.T) {
	f := &fakeServer{
		pages: []fakePage{{changes: []engine.Change{
			{Seq: 1, Op: "upsert", NodeID: "D", RelPath: "Docs", IsDir: true, Version: 1},
			{Seq: 2, Op: "upsert", NodeID: "Da", RelPath: "Docs/a.txt", Version: 1, Size: 1},
			{Seq: 3, Op: "upsert", NodeID: "d", RelPath: "docs", IsDir: true, Version: 1},
			{Seq: 4, Op: "upsert", NodeID: "db", RelPath: "docs/b.txt", Version: 1, Size: 1},
		}, next: 4}},
		download: map[string][]byte{"db": []byte("b")},
	}
	c, h := newTestController(t, f)
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	cached, err := c.Pin(context.Background(), "db")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	f.gone = map[string]bool{"D": true, "Da": true}

	if err := c.Delete(context.Background(), "D"); err != nil {
		t.Fatalf("Delete of a ghost: %v", err)
	}
	assertGone(t, h.idx, "D", "Da")
	assertKept(t, h.idx, "d", "db")
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("cached copy of the live docs/b.txt was removed: %v", err)
	}
}

// A purged node is re-announced as a delete with its ORIGINAL path. By then a new folder may
// live at that path: the delete applies to the node by id, never to whatever the feed path
// names now.
func TestPurgeOfAnOldNodeKeepsTheNewNodeAtItsPath(t *testing.T) {
	f := &fakeServer{
		pages: []fakePage{
			{changes: []engine.Change{
				{Seq: 1, Op: "upsert", NodeID: "A", RelPath: "P", IsDir: true, Version: 1},
				{Seq: 2, Op: "delete", NodeID: "A", RelPath: "P", IsDir: true, Deleted: true},
				{Seq: 3, Op: "upsert", NodeID: "B", RelPath: "P", IsDir: true, Version: 1},
				{Seq: 4, Op: "upsert", NodeID: "f", RelPath: "P/f", Version: 1, Size: 1},
			}, next: 4},
			{changes: []engine.Change{
				{Seq: 5, Op: "delete", NodeID: "A", RelPath: "P", IsDir: true, Deleted: true},
			}, next: 5},
		},
		download: map[string][]byte{"f": []byte("f")},
	}
	c, h := newTestController(t, f)
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	cached, err := c.Pin(context.Background(), "f")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh (purge): %v", err)
	}
	assertKept(t, h.idx, "B", "f")
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("cached copy of the new P/f was removed: %v", err)
	}
}

// Paths are not unique. A ghost A still indexed at P (with its child P/x) and a live B created
// at P since (with P/y): forgetting A must leave B, P/y and their cached files alone. Without
// parent ids P/x cannot be attributed to A safely, so it stays until a request about it
// answers "not found" in turn.
func TestForgettingAGhostKeepsALiveNodeAtTheSamePath(t *testing.T) {
	f := &fakeServer{
		pages: []fakePage{{changes: []engine.Change{
			{Seq: 1, Op: "upsert", NodeID: "A", RelPath: "P", IsDir: true, Version: 1},
			{Seq: 2, Op: "upsert", NodeID: "Ax", RelPath: "P/x", Version: 1, Size: 1},
			{Seq: 3, Op: "upsert", NodeID: "B", RelPath: "P", IsDir: true, Version: 1},
			{Seq: 4, Op: "upsert", NodeID: "By", RelPath: "P/y", Version: 1, Size: 1},
			{Seq: 5, Op: "upsert", NodeID: "Fa", RelPath: "f.txt", Version: 1, Size: 1},
			{Seq: 6, Op: "upsert", NodeID: "Fb", RelPath: "f.txt", Version: 2, Size: 1},
		}, next: 6}},
		download: map[string][]byte{"By": []byte("y"), "Fa": []byte("a"), "Fb": []byte("b")},
	}
	c, h := newTestController(t, f)
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	pinnedY, err := c.Pin(context.Background(), "By")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	if _, err := c.Open(context.Background(), "Fa"); err != nil {
		t.Fatalf("Open Fa: %v", err)
	}
	pinnedF, err := c.Pin(context.Background(), "Fb") // same local path as Fa's copy
	if err != nil {
		t.Fatalf("Pin Fb: %v", err)
	}
	f.gone = map[string]bool{"A": true, "Ax": true, "Fa": true}

	if err := c.Delete(context.Background(), "A"); err != nil {
		t.Fatalf("Delete of ghost A: %v", err)
	}
	if err := c.Delete(context.Background(), "Fa"); err != nil {
		t.Fatalf("Delete of ghost Fa: %v", err)
	}
	assertGone(t, h.idx, "A", "Fa")
	assertKept(t, h.idx, "B", "By", "Fb")
	for _, p := range []string{pinnedY, pinnedF} {
		if got, err := os.ReadFile(p); err != nil {
			t.Errorf("live copy %s was removed: %v", p, err)
		} else if p == pinnedF && string(got) != "b" {
			t.Errorf("live copy of f.txt = %q, want b", got)
		}
	}
}

// Opening a ghost that shares its name with a live, cached file must not truncate or remove
// the live copy: the download goes to a temporary file first.
func TestOpenOfAGhostKeepsALiveCopyOfTheSameName(t *testing.T) {
	f := &fakeServer{
		pages: []fakePage{{changes: []engine.Change{
			{Seq: 1, Op: "upsert", NodeID: "Fa", RelPath: "f.txt", Version: 1, Size: 1},
			{Seq: 2, Op: "upsert", NodeID: "Fb", RelPath: "f.txt", Version: 2, Size: 1},
		}, next: 2}},
		download: map[string][]byte{"Fb": []byte("b")},
	}
	c, h := newTestController(t, f)
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	live, err := c.Pin(context.Background(), "Fb")
	if err != nil {
		t.Fatalf("Pin: %v", err)
	}
	f.gone = map[string]bool{"Fa": true}
	if _, err := c.Open(context.Background(), "Fa"); !errors.Is(err, ErrNodeGone) {
		t.Fatalf("Open of a ghost: %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "Fa")
	assertKept(t, h.idx, "Fb")
	if got, err := os.ReadFile(live); err != nil || string(got) != "b" {
		t.Fatalf("live copy after opening the ghost: %q %v, want b", got, err)
	}
	if entries, _ := os.ReadDir(c.contentDir); len(entries) != 1 {
		t.Errorf("content dir holds %d entries, want only f.txt (no leftover temp file)", len(entries))
	}
}

// Sharing and restoring answer the same 404 for an unknown recipient or a missing version as
// for a missing node. Such an answer about a node the server still has forgets nothing.
func TestConfirmedGoneKeepsANodeTheServerStillHas(t *testing.T) {
	c, h, _ := ghostController(t)
	err := c.ForgetIfConfirmedGone(context.Background(), "keep", notFound)
	if errors.Is(err, ErrNodeGone) || err == nil {
		t.Fatalf("ForgetIfConfirmedGone = %v, want the original error", err)
	}
	assertKept(t, h.idx, "keep", "new1", "app", "plist")
}

func TestConfirmedGoneForgetsAGhost(t *testing.T) {
	c, h, f := ghostController(t)
	f.gone = map[string]bool{"app": true, "plist": true}
	if err := c.ForgetIfConfirmedGone(context.Background(), "app", notFound); !errors.Is(err, ErrNodeGone) {
		t.Fatalf("ForgetIfConfirmedGone = %v, want ErrNodeGone", err)
	}
	assertGone(t, h.idx, "app", "plist")
	assertKept(t, h.idx, "new1", "keep")
}
