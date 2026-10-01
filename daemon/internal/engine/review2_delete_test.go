package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
)

// Round 4: a feed delete removes the deleted node and its descendants by identity, never
// whatever else sits at or under its local path.

type row struct {
	n       index.Node
	seq     int64
	content string // written to disk for a file; "" leaves disk alone
}

// indexRows indexes rows as an earlier pull (or push, or an older client version) left
// them, with their files on disk; the source serves the given deletes from seq 10 on.
func indexRows(t *testing.T, rows []row, deletes ...Change) (*Engine, string) {
	t.Helper()
	return indexRowsOn(t, &fakeSource{changes: deletes}, rows)
}

// indexRowsOn is indexRows over a given source.
func indexRowsOn(t *testing.T, src Source, rows []row) (*Engine, string) {
	t.Helper()
	e, root := newEngine(t, src)
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	if err := e.idx.SetCursor(9); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		n := r.n
		if !n.IsDir && r.content != "" {
			n.ContentHash, n.Size = hashOf([]byte(r.content)), int64(len(r.content))
		}
		if err := e.idx.Put(n); err != nil {
			t.Fatal(err)
		}
		if err := e.idx.SetSeq(n.NodeID, r.seq); err != nil {
			t.Fatal(err)
		}
		local := n.LocalPath
		if local == "" {
			local = n.RelPath
		}
		p := filepath.Join(root, filepath.FromSlash(local))
		if n.IsDir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if r.content != "" {
			writeAt(t, p, r.content)
		}
	}
	return e, root
}

func pullDeletes(t *testing.T, e *Engine) {
	t.Helper()
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
}

// deleteFixture is indexRows followed by the pull that applies the deletes.
func deleteFixture(t *testing.T, rows []row, deletes ...Change) (*Engine, string) {
	t.Helper()
	e, root := indexRows(t, rows, deletes...)
	pullDeletes(t, e)
	return e, root
}

func del(seq int64, id, rel string, dir bool) Change {
	return Change{Seq: seq, NodeID: id, RelPath: rel, IsDir: dir, Deleted: true}
}

func wantRow(t *testing.T, e *Engine, id string, want bool) {
	t.Helper()
	if _, ok, _ := e.idx.Get(id); ok != want {
		t.Errorf("%s indexed = %v, want %v", id, ok, want)
	}
}

// A live folder took the ghost's server path, and a file of the live folder sits in the
// ghost's local folder (placed there by an older version, or by the fallback while the live
// folder was unknown). The ghost's late delete takes only the ghost; the ghost's own child
// goes with its own delete row, which the server sends for every purged node.
func TestGhostDeleteKeepsLiveFileInItsFolder(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "b", RelPath: "P", LocalPath: localname.Disambiguate("P", "b"), IsDir: true, Version: 1}, seq: 5},
		{n: index.Node{NodeID: "c", RelPath: "P/x.md", LocalPath: "P/x.md", Version: 1}, seq: 6, content: "live"},
		{n: index.Node{NodeID: "g", RelPath: "P/old.md", LocalPath: "P/old.md", Version: 1}, seq: 3, content: "ghost child"},
	}, del(10, "a", "P", true), del(11, "g", "P/old.md", false))
	wantRow(t, e, "a", false)
	wantRow(t, e, "g", false)
	wantRow(t, e, "c", true)
	wantRow(t, e, "b", true)
	// c was placed after the live folder b: it is b's, and moves into b's folder (round 7).
	bl := localname.Disambiguate("P", "b")
	mustRead(t, filepath.Join(root, bl, "x.md"), "live")
	wantLocal(t, e, "c", bl+"/x.md")
	mustBeGone(t, filepath.Join(root, "P"))
}

// Another node records the ghost's exact local path (a push that answered with the live
// node's id for the same path): the file is that node's, and only the ghost's row goes.
func TestGhostDeleteKeepsFileSharedWithLiveNode(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "x.md", LocalPath: "x.md", Version: 1}, seq: 2, content: "live"},
		// Written by a push: seq one past the cursor (9), see putPushed.
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 3}, seq: 10, content: "live"},
	}, del(10, "a", "x.md", false))
	wantRow(t, e, "a", false)
	wantRow(t, e, "b", true)
	mustRead(t, filepath.Join(root, "x.md"), "live")
}

// A folder delete takes its descendants by server path and local path together: a node
// that merely sits under the folder on disk while its server path is elsewhere stays, and
// so do edited and never-synced files.
func TestFolderDeleteTakesOnlyItsDescendants(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "d", RelPath: "P/d.md", LocalPath: "P/d.md", Version: 1}, seq: 3, content: "synced"},
		{n: index.Node{NodeID: "s", RelPath: "P/sub", LocalPath: "P/sub", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "s1", RelPath: "P/sub/s.md", LocalPath: "P/sub/s.md", Version: 1}, seq: 3, content: "deep"},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/z.md", Version: 1}, seq: 4, content: "elsewhere"},
		{n: index.Node{NodeID: "ed", RelPath: "P/e.md", LocalPath: "P/e.md", Version: 1}, seq: 3, content: "old"},
	}, del(10, "a", "P", true))
	writeAt(t, filepath.Join(root, "P", "e.md"), "edited")
	writeAt(t, filepath.Join(root, "P", "u.md"), "unsynced")
	writeAt(t, filepath.Join(root, "P", "sub", ".DS_Store"), "junk")
	pullDeletes(t, e)
	for _, id := range []string{"d", "s", "s1", "ed"} {
		wantRow(t, e, id, false)
	}
	wantRow(t, e, "z", true)
	// z is not the folder's. Its server folder Q is not indexed, so it stays where it is
	// (round 7: no folder is created for it). The folder stays for it and for the edited
	// and unsynced files, without its row, and those go up as new.
	wantRow(t, e, "a", false)
	wantLocal(t, e, "z", "P/z.md")
	mustRead(t, filepath.Join(root, "P", "z.md"), "elsewhere")
	mustRead(t, filepath.Join(root, "P", "e.md"), "edited")
	mustRead(t, filepath.Join(root, "P", "u.md"), "unsynced")
	mustBeGone(t, filepath.Join(root, "P", "d.md"))
	mustBeGone(t, filepath.Join(root, "P", "sub"))
}

// Plain folder delete, nothing shared: everything synced goes, the folder too.
func TestFolderDeleteRemovesSyncedTree(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "s", RelPath: "P/sub", LocalPath: "P/sub", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "s1", RelPath: "P/sub/s.md", LocalPath: "P/sub/s.md", Version: 1}, seq: 3, content: "deep"},
	}, del(10, "a", "P", true))
	for _, id := range []string{"a", "s", "s1"} {
		wantRow(t, e, id, false)
	}
	mustBeGone(t, filepath.Join(root, "P"))
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// Round 5: ownership by seq. The reviewer's probes, kept as regression tests.

func fileChange(seq int64, id, rel, body string) Change {
	return Change{Seq: seq, Op: "create", NodeID: id, RelPath: rel, Version: 1, ContentHash: hashOf([]byte(body)), Size: int64(len(body))}
}

func dirChange(seq int64, id, rel string) Change {
	return Change{Seq: seq, Op: "create", NodeID: id, RelPath: rel, IsDir: true, Version: 1}
}

func wantClean(t *testing.T, e *Engine) {
	t.Helper()
	if all, _ := e.idx.All(); len(all) != 0 {
		t.Errorf("rows left: %+v", all)
	}
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Errorf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// The server sends one delete row for a deleted folder; its whole tree goes.
func TestRootOnlyDeleteRealPull(t *testing.T) {
	src := &fakeSource{
		changes: []Change{
			dirChange(1, "D", "D"),
			dirChange(2, "S", "D/sub"),
			dirChange(3, "SS", "D/sub/deeper"),
			fileChange(4, "f1", "D/a.md", "a"),
			fileChange(5, "f2", "D/sub/b.md", "b"),
			fileChange(6, "f3", "D/sub/deeper/c.md", "c"),
			dirChange(7, "E", "D/empty"),
		},
		bodies: map[string][][]byte{"f1": {[]byte("a")}, "f2": {[]byte("b")}, "f3": {[]byte("c")}},
	}
	e, root := newEngine(t, src)
	pullDeletes(t, e)
	src.changes = append(src.changes, del(8, "D", "D", true))
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "D"))
	wantClean(t, e)
}

// The same over rows from before seqs were recorded.
func TestRootOnlyDeleteLegacySeq0(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "D", LocalPath: "D", IsDir: true, Version: 1}},
		{n: index.Node{NodeID: "S", RelPath: "D/sub", LocalPath: "D/sub", IsDir: true, Version: 1}},
		{n: index.Node{NodeID: "f1", RelPath: "D/a.md", LocalPath: "D/a.md", Version: 1}, content: "a"},
		{n: index.Node{NodeID: "f2", RelPath: "D/sub/b.md", LocalPath: "D/sub/b.md", Version: 1}, content: "b"},
	}, del(10, "D", "D", true))
	mustBeGone(t, filepath.Join(root, "D"))
	wantClean(t, e)
}

// A server move (a row per subtree node) followed by a root-only delete.
func TestMoveThenRootOnlyDelete(t *testing.T) {
	src := &fakeSource{
		changes: []Change{
			dirChange(1, "D", "D"),
			dirChange(2, "S", "D/sub"),
			fileChange(3, "f1", "D/sub/b.md", "b"),
			dirChange(4, "X", "X"),
		},
		bodies: map[string][][]byte{"f1": {[]byte("b")}},
	}
	e, root := newEngine(t, src)
	pullDeletes(t, e)
	mv := fileChange(7, "f1", "X/D2/sub/b.md", "b")
	mv.Op = "update"
	src.changes = append(src.changes,
		Change{Seq: 5, Op: "move", NodeID: "D", RelPath: "X/D2", IsDir: true, Version: 2},
		Change{Seq: 6, Op: "update", NodeID: "S", RelPath: "X/D2/sub", IsDir: true, Version: 1},
		mv,
		del(8, "D", "X/D2", true))
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "X", "D2"))
	mustBeGone(t, filepath.Join(root, "D"))
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// An ordinary folder delete whose folder also holds two rows for one child (a ghost and the
// live node): the folder owns both, everything goes and nothing is pushed back.
func TestFolderDeleteTakesDuplicateChildRows(t *testing.T) {
	for _, seqs := range [][3]int64{{0, 0, 0}, {1, 2, 3}} {
		e, root := deleteFixture(t, []row{
			{n: index.Node{NodeID: "P", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: seqs[0]},
			{n: index.Node{NodeID: "g", RelPath: "P/x.md", LocalPath: "P/x (g).md", Version: 1}, seq: seqs[1], content: "old"},
			{n: index.Node{NodeID: "l", RelPath: "P/x.md", LocalPath: "P/x.md", Version: 1}, seq: seqs[2], content: "live"},
		}, del(10, "P", "P", true))
		mustBeGone(t, filepath.Join(root, "P"))
		wantClean(t, e)
	}
}

// The live file is deleted on the server while a ghost row still records its local path:
// the live node owns the path, the file goes, and the ghost's row with it (its own purge
// row is then a no-op). Before, the file stayed and was pushed back.
func TestLiveDeleteWhileGhostSharesLocalPath(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "g", RelPath: "P.md", LocalPath: "P.md", Version: 1, ContentHash: hashOf([]byte("ghost")), Size: 5}, seq: 2},
		{n: index.Node{NodeID: "b", RelPath: "P.md", LocalPath: "P.md", Version: 1}, seq: 5, content: "live"},
	}, del(10, "b", "P.md", false), del(11, "g", "P.md", false))
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "P.md"))
	wantClean(t, e)
}

// The same for folders: the live folder's delete takes its tree and the ghost's row.
func TestLiveFolderDeleteWhileGhostSharesLocalPath(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "g", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}, seq: 5},
		{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: "A/x.md", Version: 1}, seq: 6, content: "live child"},
	}, del(10, "b", "A", true))
	mustBeGone(t, filepath.Join(root, "A"))
	wantClean(t, e)
}

// A descendant by server path whose local path is elsewhere (a sticky local name) is the
// deleted folder's too.
func TestFolderDeleteTakesDescendantPlacedElsewhere(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "Docs", LocalPath: "docs", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "f", RelPath: "Docs/a.md", LocalPath: "docs/a.md", Version: 1}, seq: 2, content: "a"},
		{n: index.Node{NodeID: "h", RelPath: "Docs/h.md", LocalPath: "Old/h.md", Version: 1}, seq: 2, content: "h"},
	}, del(10, "D", "Docs", true))
	mustBeGone(t, filepath.Join(root, "docs"))
	mustBeGone(t, filepath.Join(root, "Old", "h.md"))
	wantRow(t, e, "h", false)
}

// A row a push writes counts as placed after everything the feed delivered so far.
func TestPushRecordsSeqPastCursor(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	pullDeletes(t, e) // establishes the mirror
	if err := e.idx.SetCursor(41); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "new.md"), "new")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	all, _ := e.idx.All()
	if len(all) != 1 {
		t.Fatalf("rows = %+v, want the pushed file", all)
	}
	seqs, _ := e.idx.Seqs()
	if got := seqs[all[0].NodeID]; got != 42 {
		t.Fatalf("pushed row seq = %d, want 42", got)
	}
}

// Round 6: the reviewer's round-5 probes, kept as regression tests.

// liveDirSink answers EnsureDir with the id of a folder the server already has at that
// path, as the real server does (EnsureDir is idempotent).
type liveDirSink struct {
	*fakeSink
	ids map[string]string
}

func (s liveDirSink) EnsureDir(ctx context.Context, rel string) (RemoteNode, error) {
	s.fakeSink.EnsureDir(ctx, rel)
	if id, ok := s.ids[rel]; ok {
		return RemoteNode{NodeID: id, Version: 1}, nil
	}
	return RemoteNode{NodeID: "dir-" + rel, Version: 1}, nil
}

func wantLocal(t *testing.T, e *Engine, id, local string) {
	t.Helper()
	n, ok, _ := e.idx.Get(id)
	if !ok || n.LocalPath != local {
		t.Errorf("%s local path = %q (indexed %v), want %q", id, n.LocalPath, ok, local)
	}
}

func wantNoLocalChanges(t *testing.T, e *Engine) {
	t.Helper()
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Errorf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// C-1: a ghost folder's purge rows, in either order (the server writes them parents before
// children), leave nothing behind: no rowless folder for the push to create on the server,
// and the live folder's row is not re-pointed at the ghost's folder.
func TestGhostFolderPurgeLeavesNothingToPush(t *testing.T) {
	bl := localname.Disambiguate("A", "b")
	for _, parentFirst := range []bool{true, false} {
		deletes := []Change{del(10, "g", "A", true), del(11, "gc", "A/old.md", false)}
		if !parentFirst {
			deletes = []Change{del(10, "gc", "A/old.md", false), del(11, "g", "A", true)}
		}
		e, root := deleteFixture(t, []row{
			{n: index.Node{NodeID: "g", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}, seq: 2},
			{n: index.Node{NodeID: "gc", RelPath: "A/old.md", LocalPath: "A/old.md", Version: 1}, seq: 3, content: "old"},
			{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: bl, IsDir: true, Version: 1}, seq: 5},
			{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: bl + "/x.md", Version: 1}, seq: 6, content: "live"},
		}, deletes...)
		mustBeGone(t, filepath.Join(root, "A"))
		mustRead(t, filepath.Join(root, bl, "x.md"), "live")
		wantRow(t, e, "g", false)
		wantRow(t, e, "gc", false)
		wantNoLocalChanges(t, e)
		s := liveDirSink{newFakeSink(), map[string]string{"A": "b"}}
		if err := e.PushLocal(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		if len(s.ops) != 0 {
			t.Errorf("parentFirst=%v: push ops %v, want none", parentFirst, s.ops)
		}
		wantLocal(t, e, "b", bl)
		wantLocal(t, e, "c", bl+"/x.md")
	}
}

// C-1, the residual: a ghost's folder that still holds a live file stays on disk without a
// row. The push may ask the server for that folder, but an answer naming a folder the index
// already has elsewhere never re-points that folder's row.
func TestPushNeverRebindsAnIndexedFolder(t *testing.T) {
	bl := localname.Disambiguate("P", "b")
	e, _ := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "b", RelPath: "P", LocalPath: bl, IsDir: true, Version: 1}, seq: 5},
		// Placed before b (an older version's placement): it cannot be told from a's.
		{n: index.Node{NodeID: "c", RelPath: "P/x.md", LocalPath: "P/x.md", Version: 1}, seq: 1, content: "live"},
	}, del(10, "a", "P", true))
	s := liveDirSink{newFakeSink(), map[string]string{"P": "b"}}
	for i := 0; i < 2; i++ {
		if err := e.PushLocal(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	wantLocal(t, e, "b", bl)
	wantLocal(t, e, "c", "P/x.md")
	if len(s.deleted) != 0 || len(s.pushed) != 0 {
		t.Errorf("push ops %v, want at most EnsureDir", s.ops)
	}
}

// I-2: a file that cannot be read or removed while its folder is deleted keeps the rows
// needed to retry: the folder's own row goes last, and the retry finishes the delete.
func TestPartialFolderDeleteRetries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "D", LocalPath: "D", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "f1", RelPath: "D/a.md", LocalPath: "D/a.md", Version: 1}, seq: 2, content: "a"},
		{n: index.Node{NodeID: "f2", RelPath: "D/b.md", LocalPath: "D/b.md", Version: 1}, seq: 3, content: "b"},
	}, del(10, "D", "D", true))
	p := filepath.Join(root, "D", "a.md")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatal("first pull: want the unreadable file's error")
	}
	wantRow(t, e, "D", true)
	wantRow(t, e, "f1", true)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	wantNoLocalChanges(t, e) // nothing to push back before the retry
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "D"))
	wantClean(t, e)
}

// I-2 with an injected removal failure (a file held open on Windows): the rest of the set is
// still handled, the failed file and the rows above it stay, and the retry completes.
func TestFolderDeleteRemovalFailureRetries(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "D", LocalPath: "D", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "S", RelPath: "D/sub", LocalPath: "D/sub", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "f1", RelPath: "D/sub/a.md", LocalPath: "D/sub/a.md", Version: 1}, seq: 3, content: "a"},
		{n: index.Node{NodeID: "f2", RelPath: "D/b.md", LocalPath: "D/b.md", Version: 1}, seq: 4, content: "b"},
	}, del(10, "D", "D", true))
	held := filepath.Join(root, "D", "sub", "a.md")
	orig := removeFile
	t.Cleanup(func() { removeFile = orig })
	removeFile = func(p string) error {
		if p == held {
			return &os.PathError{Op: "remove", Path: p, Err: os.ErrPermission}
		}
		return orig(p)
	}
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatal("first pull: want the removal error")
	}
	mustRead(t, held, "a")
	mustBeGone(t, filepath.Join(root, "D", "b.md"))
	for _, id := range []string{"D", "S", "f1"} {
		wantRow(t, e, id, true)
	}
	wantRow(t, e, "f2", false)
	wantNoLocalChanges(t, e)
	removeFile = orig
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "D"))
	wantClean(t, e)
}

// I-3: a node sitting on disk in a deleted folder while its server path is elsewhere moves
// to where its server folder is, and the deleted folder goes with its row. Nothing stale is
// left to re-route the user's new files or to delete a live folder by path.
func TestStrayMovesOutOfDeletedFolder(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/z.md", Version: 1}, seq: 4, content: "elsewhere"},
		{n: index.Node{NodeID: "q", RelPath: "Q", LocalPath: "Q", IsDir: true, Version: 1}, seq: 4},
	}, del(10, "a", "P", true))
	wantRow(t, e, "a", false)
	wantLocal(t, e, "z", "Q/z.md")
	mustRead(t, filepath.Join(root, "Q", "z.md"), "elsewhere")
	mustBeGone(t, filepath.Join(root, "P"))
	wantNoLocalChanges(t, e)

	// The user makes a new P; the server makes it a new folder, and its echo lands in place.
	writeAt(t, filepath.Join(root, "P", "new.md"), "new")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	src := e.src.(*fakeSource)
	src.changes = append(src.changes,
		Change{Seq: 20, Op: "create", NodeID: "dir-P", RelPath: "P", IsDir: true, Version: 1},
		Change{Seq: 21, Op: "create", NodeID: "srv-P/new.md", RelPath: "P/new.md", Version: 10, ContentHash: hashOf([]byte("new")), Size: 3})
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "P", "new.md"), "new")
	wantLocal(t, e, "dir-P", "P")
	if len(sink.deleted) != 0 {
		t.Errorf("push deleted %v", sink.deleted)
	}
	wantNoLocalChanges(t, e)
}

// I-3: a stray that cannot be moved (its server folder Z is not indexed) keeps its place,
// and the deleted folder holding it stays on disk but loses its row: a row kept there could
// later delete a live server folder by path.
func TestUnmovableStrayDropsFolderRow(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "s", RelPath: "Z/B", LocalPath: "P/s", IsDir: true, Version: 1}, seq: 4},
		{n: index.Node{NodeID: "s1", RelPath: "Z/B/s.md", LocalPath: "P/s/s.md", Version: 1}, seq: 4, content: "stray"},
	}, del(10, "a", "P", true))
	wantRow(t, e, "a", false)
	wantRow(t, e, "s", true)
	mustRead(t, filepath.Join(root, "P", "s", "s.md"), "stray")
	// The user removes the folder: the stray's own rows report deletes, never P.
	if err := os.RemoveAll(filepath.Join(root, "P")); err != nil {
		t.Fatal(err)
	}
	got, _ := e.DetectLocal()
	for _, c := range got {
		if c.RelPath == "P" {
			t.Errorf("DetectLocal reports the deleted folder: %+v", c)
		}
	}
}

// M-1: a push while the cursor is held behind a failing change still places the pushed
// row after every row the pull applied.
func TestPushSeqPastAppliedRows(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "g", RelPath: "n.md", LocalPath: "n.md", Version: 1}, seq: 50, content: "v1"},
	})
	writeAt(t, filepath.Join(root, "n.md"), "v2")
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatal(err)
	}
	seqs, _ := e.idx.Seqs()
	if got := seqs["srv-n.md"]; got != 51 {
		t.Fatalf("pushed row seq = %d, want 51", got)
	}
	e.src.(*fakeSource).changes = []Change{del(60, "g", "n.md", false)}
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "n.md"), "v2")
	wantRow(t, e, "srv-n.md", true)
	wantNoLocalChanges(t, e)
}

// M-2: legacy rows at two case variants of one path, one file on a case-insensitive disk.
// The ghost's purge must leave the live node's file, or the push deletes it on the server.
func TestCaseVariantGhostPurgeKeepsLiveFile(t *testing.T) {
	probe := t.TempDir()
	writeAt(t, filepath.Join(probe, "a"), "")
	if _, err := os.Stat(filepath.Join(probe, "A")); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "N", RelPath: "N", LocalPath: "N", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "g", RelPath: "N/X.md", LocalPath: "N/X.md", Version: 1}, seq: 3, content: "same"},
		{n: index.Node{NodeID: "b", RelPath: "N/x.md", LocalPath: "N/x.md", Version: 1}, seq: 5, content: "same"},
	}, del(10, "g", "N/X.md", false))
	mustRead(t, filepath.Join(root, "N", "x.md"), "same")
	wantRow(t, e, "b", true)
	wantNoLocalChanges(t, e)
}

// M-4: a deleted node whose local path is not inside the root (a symlinked component)
// keeps its row and the disk, and the pull goes on instead of failing forever.
func TestDeleteWithRejectedLocalPathDoesNotStickCursor(t *testing.T) {
	outside := t.TempDir()
	writeAt(t, filepath.Join(outside, "x.md"), "outside")
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "n", RelPath: "L/x.md", LocalPath: "L/x.md", Version: 1, ContentHash: hashOf([]byte("outside")), Size: 7}, seq: 2},
	}, del(10, "n", "L/x.md", false))
	if err := os.Symlink(outside, filepath.Join(root, "L")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	pullDeletes(t, e)
	mustRead(t, filepath.Join(outside, "x.md"), "outside")
	wantRow(t, e, "n", true)
	if c, _ := e.idx.Cursor(); c != 10 {
		t.Errorf("cursor = %d, want 10", c)
	}
}

// Round 7: the reviewer's round-6 probes, kept as regression tests. (Their seq-tie cases
// are settled by the server since round 8; see the round-8 section.)

func existsAt(p string) bool { _, err := os.Lstat(p); return err == nil }

// R6-3: a stray with an unsynced edit moves intact and is reported as an update.
func TestStrayWithUnsyncedEditMovesIntact(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "q", RelPath: "Q", LocalPath: "Q", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/z.md", Version: 1}, seq: 4, content: "synced"},
	}, del(10, "a", "P", true))
	writeAt(t, filepath.Join(root, "P", "z.md"), "unsynced edit")
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "Q", "z.md"), "unsynced edit")
	if got, _ := e.DetectLocal(); len(got) != 1 || got[0].Op != "update" {
		t.Errorf("DetectLocal = %+v, want one update", got)
	}
}

// R6-4: the stray's place is taken by the user's file in another case: it gets a
// generated name, and the user's file is untouched.
func TestStrayTargetTakenGetsGeneratedName(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "q", RelPath: "Q", LocalPath: "Q", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/z.md", Version: 1}, seq: 4, content: "stray"},
	}, del(10, "a", "P", true))
	writeAt(t, filepath.Join(root, "Q", "Z.md"), "user file")
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "Q", "Z.md"), "user file")
	n, _, _ := e.idx.Get("z")
	mustRead(t, filepath.Join(root, filepath.FromSlash(n.LocalPath)), "stray")
}

// R6-5: a stray folder (with its server folder's contents) inside the deleted folder moves
// as a whole, and the deleted folder goes: nothing is pushed back.
func TestStrayFolderMovesWhole(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "q", RelPath: "Q", LocalPath: "P/Q", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/Q/z.md", Version: 1}, seq: 4, content: "stray"},
	}, del(10, "a", "P", true))
	mustRead(t, filepath.Join(root, "Q", "z.md"), "stray")
	wantLocal(t, e, "q", "Q")
	wantLocal(t, e, "z", "Q/z.md")
	mustBeGone(t, filepath.Join(root, "P"))
	wantNoLocalChanges(t, e)
}

// M-2: a stray whose server folder is not indexed stays where it is: moving it would create
// that folder locally, and the push would create it on the server.
func TestStrayWithUnindexedFolderStays(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "z", RelPath: "Q/z.md", LocalPath: "P/z.md", Version: 1}, seq: 4, content: "stray"},
	}, del(10, "a", "P", true))
	mustRead(t, filepath.Join(root, "P", "z.md"), "stray")
	wantLocal(t, e, "z", "P/z.md")
	if existsAt(filepath.Join(root, "Q")) {
		t.Error("the unindexed server folder Q was created locally")
	}
}

// R6-6: the user's new empty folders survive server deletes around them.
func TestUserEmptyFoldersSurviveDeletes(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "D", LocalPath: "D", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "f", RelPath: "D/x.md", LocalPath: "D/x.md", Version: 1}, seq: 2, content: "x"},
		{n: index.Node{NodeID: "E", RelPath: "E", LocalPath: "E", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "h", RelPath: "E/y.md", LocalPath: "E/y.md", Version: 1}, seq: 2, content: "y"},
	}, del(10, "D", "D", true), del(11, "h", "E/y.md", false))
	for _, p := range []string{"D/keep", "E/keep2", "Top"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pullDeletes(t, e)
	for _, p := range []string{"D/keep", "E/keep2", "Top", "E"} {
		if !existsAt(filepath.Join(root, p)) {
			t.Errorf("user's folder %s removed", p)
		}
	}
}

// R6-7: a rowless folder that only held a ghost's file goes with it.
func TestGhostPurgeEmptiesRowlessFolder(t *testing.T) {
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "g", RelPath: "U/old.md", LocalPath: "U/old.md", Version: 1}, seq: 2, content: "old"},
	}, del(10, "g", "U/old.md", false))
	mustBeGone(t, filepath.Join(root, "U"))
	wantClean(t, e)
}

// renameBackFixture: live folder b took a generated name while a ghost held A; the ghost is
// gone, and the user renames b's folder back to A.
func renameBackFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	bl := localname.Disambiguate("A", "b")
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: bl, IsDir: true, Version: 1}, seq: 5},
		{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: bl + "/x.md", Version: 1}, seq: 6, content: "live"},
	})
	if err := os.Rename(filepath.Join(root, bl), filepath.Join(root, "A")); err != nil {
		t.Fatal(err)
	}
	return e, root
}

// plainSink is a Sink without server-side moves.
type plainSink struct{ s *fakeSink }

func (p plainSink) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, m time.Time) (RemoteNode, bool, error) {
	if rel == "A/x.md" || rel == "x.md" {
		b, _ := io.ReadAll(r)
		p.s.ops = append(p.s.ops, "push "+rel)
		id := map[string]string{"A/x.md": "c", "x.md": "b"}[rel]
		return RemoteNode{NodeID: id, Version: 2, Hash: hashOf(b)}, false, nil
	}
	return p.s.PushFile(ctx, rel, base, r, m)
}
func (p plainSink) EnsureDir(ctx context.Context, rel string) (RemoteNode, error) {
	p.s.ops = append(p.s.ops, "dir "+rel)
	if rel == "A" {
		return RemoteNode{NodeID: "b", Version: 1}, nil
	}
	return p.s.EnsureDir(ctx, rel)
}
func (p plainSink) DeleteRemote(ctx context.Context, rel string) error {
	return p.s.DeleteRemote(ctx, rel)
}

// R6-8: renaming a generated folder name back to the plain one (the server path) is a local
// rename only: the rows follow it, and the live folder is not deleted on the server.
func TestRenameGeneratedFolderBack(t *testing.T) {
	for _, moves := range []bool{true, false} {
		e, root := renameBackFixture(t)
		fs := newFakeSink()
		var sink Sink = liveDirSink{fs, map[string]string{"A": "b"}}
		if !moves {
			sink = plainSink{fs}
		}
		if err := e.PushLocal(context.Background(), sink); err != nil {
			t.Fatal(err)
		}
		if len(fs.deleted) != 0 {
			t.Errorf("moves=%v: DeleteRemote %v (ops %v)", moves, fs.deleted, fs.ops)
		}
		wantLocal(t, e, "b", "A")
		wantLocal(t, e, "c", "A/x.md")
		mustRead(t, filepath.Join(root, "A", "x.md"), "live")
		wantNoLocalChanges(t, e)
	}
}

// The file analogue: x~<hash>.md renamed back to x.md.
func TestRenameGeneratedFileBack(t *testing.T) {
	for _, moves := range []bool{true, false} {
		bl := localname.Disambiguate("x.md", "b")
		e, root := indexRows(t, []row{
			{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: bl, Version: 1}, seq: 5, content: "live"},
		})
		if err := os.Rename(filepath.Join(root, bl), filepath.Join(root, "x.md")); err != nil {
			t.Fatal(err)
		}
		fs := newFakeSink()
		var sink Sink = fs
		if !moves {
			sink = plainSink{fs}
		}
		if err := e.PushLocal(context.Background(), sink); err != nil {
			t.Fatal(err)
		}
		if len(fs.deleted) != 0 {
			t.Errorf("moves=%v: DeleteRemote %v (ops %v)", moves, fs.deleted, fs.ops)
		}
		wantLocal(t, e, "b", "x.md")
		mustRead(t, filepath.Join(root, "x.md"), "live")
		wantNoLocalChanges(t, e)
	}
}

// R6-9: a doomed file that stays locked holds the cursor; later changes still apply, the
// rows needed for the retry stay, nothing is pushed in between, and the unlock finishes.
func TestPermanentRemovalFailureHoldsCursor(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "D", LocalPath: "D", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "S", RelPath: "D/S", LocalPath: "D/S", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "f1", RelPath: "D/S/a.md", LocalPath: "D/S/a.md", Version: 1}, seq: 2, content: "a"},
		{n: index.Node{NodeID: "f2", RelPath: "D/b.md", LocalPath: "D/b.md", Version: 1}, seq: 3, content: "b"},
		{n: index.Node{NodeID: "T", RelPath: "D/T", LocalPath: "D/T", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "f3", RelPath: "D/T/c.md", LocalPath: "D/T/c.md", Version: 1}, seq: 3, content: "c"},
	}, del(10, "D", "D", true), fileChange(11, "n", "new.md", "new"))
	e.src.(*fakeSource).bodies = map[string][][]byte{"n": {[]byte("new"), []byte("new"), []byte("new"), []byte("new")}}
	orig := removeFile
	t.Cleanup(func() { removeFile = orig })
	removeFile = func(p string) error {
		if filepath.Base(p) == "a.md" {
			return errors.New("locked")
		}
		return orig(p)
	}
	for i := 0; i < 3; i++ {
		if err := e.PullOnce(context.Background()); err == nil {
			t.Fatalf("pull %d: want the lock error", i)
		}
		if c, _ := e.idx.Cursor(); c != 9 {
			t.Fatalf("pull %d: cursor = %d, want 9", i, c)
		}
	}
	for _, id := range []string{"D", "S", "f1", "n"} {
		wantRow(t, e, id, true)
	}
	for _, id := range []string{"f2", "T", "f3"} {
		wantRow(t, e, id, false)
	}
	wantNoLocalChanges(t, e)
	removeFile = orig
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "D"))
	if c, _ := e.idx.Cursor(); c != 11 {
		t.Errorf("cursor = %d, want 11", c)
	}
}

// R6-10: a ghost folder holding a file of the live folder that took its server path: the
// file was placed after the live folder, so it moves into it, and the ghost's folder goes.
// No EnsureDir on every pass, no new files bounced.
func TestGhostFolderPurgeMovesLiveFileToLiveFolder(t *testing.T) {
	bl := localname.Disambiguate("P", "b")
	e, root := deleteFixture(t, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "b", RelPath: "P", LocalPath: bl, IsDir: true, Version: 1}, seq: 5},
		{n: index.Node{NodeID: "c", RelPath: "P/x.md", LocalPath: "P/x.md", Version: 1}, seq: 6, content: "live"},
		{n: index.Node{NodeID: "gc", RelPath: "P/old.md", LocalPath: "P/old.md", Version: 1}, seq: 3, content: "ghost child"},
	}, del(10, "a", "P", true), del(11, "gc", "P/old.md", false))
	mustRead(t, filepath.Join(root, bl, "x.md"), "live")
	wantLocal(t, e, "c", bl+"/x.md")
	mustBeGone(t, filepath.Join(root, "P"))
	s := liveDirSink{newFakeSink(), map[string]string{"P": "b"}}
	for i := 0; i < 3; i++ {
		if err := e.PushLocal(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.ops) != 0 {
		t.Errorf("push ops %v, want none", s.ops)
	}
}

// R6-12: ordinary two-way sync is unchanged.
func TestOrdinaryRoundTrip(t *testing.T) {
	src := &fakeSource{
		changes: []Change{dirChange(1, "d", "D"), fileChange(2, "f", "D/f.md", "f"), fileChange(3, "g", "g.md", "g")},
		bodies:  map[string][][]byte{"f": {[]byte("f")}, "g": {[]byte("g")}},
	}
	e, root := newEngine(t, src)
	pullDeletes(t, e)
	mv := dirChange(4, "d", "E")
	mv.Op = "move"
	child := fileChange(5, "f", "E/f.md", "f")
	child.Op = "update" // the server sends a row for every node of a moved subtree
	src.changes = append(src.changes, mv, child, del(6, "g", "g.md", false))
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "E", "f.md"), "f")
	mustBeGone(t, filepath.Join(root, "g.md"))
	mustBeGone(t, filepath.Join(root, "D"))
	wantNoLocalChanges(t, e)
	if err := os.Rename(filepath.Join(root, "E", "f.md"), filepath.Join(root, "E", "h.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "N"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newFakeSink()
	if err := e.PushLocal(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.deleted) != 0 || len(s.renamed) != 1 {
		t.Errorf("ops %v", s.ops)
	}
	wantNoLocalChanges(t, e)
}

// Round 8: a seq tie between a deleted node and a rival is settled by the server. Legacy
// rows (seq 0) carry no order the client can trust: the rowid is the order the rows were
// learned in, not the order they were moved in.

// checkingSource is a source that also answers NodeExists, as the protocol client does.
type checkingSource struct {
	*fakeSource
	live  map[string]bool  // ids the server still has
	fail  map[string]error // ids whose check fails (network, 5xx)
	calls map[string]int
}

func (s *checkingSource) NodeExists(_ context.Context, id string) (bool, error) {
	s.calls[id]++
	if err := s.fail[id]; err != nil {
		return false, err
	}
	return s.live[id], nil
}

func (s *checkingSource) totalCalls() int {
	n := 0
	for _, c := range s.calls {
		n += c
	}
	return n
}

func server(live ...string) *checkingSource {
	s := &checkingSource{fakeSource: &fakeSource{}, live: map[string]bool{}, fail: map[string]error{}, calls: map[string]int{}}
	for _, id := range live {
		s.live[id] = true
	}
	return s
}

// serverFixture indexes rows over a server that still has the live ids, then pulls deletes.
func serverFixture(t *testing.T, srv *checkingSource, rows []row, deletes ...Change) (*Engine, string) {
	t.Helper()
	srv.changes = deletes
	e, root := indexRowsOn(t, srv, rows)
	pullDeletes(t, e)
	return e, root
}

// The external reviewer's case: the client learned B, then A. On the server A was deleted
// and B moved onto A's path; the old client applied the move, so both rows sit at A's
// path with seq 0 and B has the lower rowid. A's delete must take only A's row: B is live.
func TestReviewLegacyMovedOlderIDSurvivesGhostPurge(t *testing.T) {
	for _, sameContent := range []bool{false, true} {
		aBody := "a's old content"
		if sameContent {
			aBody = "b's content"
		}
		srv := server("B")
		e, root := serverFixture(t, srv, []row{
			{n: index.Node{NodeID: "B", RelPath: "a.md", LocalPath: "a.md", Version: 2}, content: "b's content"},
			{n: index.Node{NodeID: "A", RelPath: "a.md", LocalPath: "a.md", Version: 1, ContentHash: hashOf([]byte(aBody)), Size: int64(len(aBody))}},
		}, del(10, "A", "a.md", false))
		mustRead(t, filepath.Join(root, "a.md"), "b's content")
		wantRow(t, e, "B", true)
		wantRow(t, e, "A", false)
		wantNoLocalChanges(t, e)
		if srv.calls["B"] != 1 {
			t.Errorf("NodeExists(B) calls = %d, want 1", srv.calls["B"])
		}
	}
}

// The folder variant: B moved onto A's path with its children.
func TestReviewLegacyMovedOlderFolderSurvivesGhostPurge(t *testing.T) {
	e, root := serverFixture(t, server("B", "c"), []row{
		{n: index.Node{NodeID: "B", RelPath: "A", LocalPath: "A", IsDir: true, Version: 2}},
		{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: "A/x.md", Version: 1}, content: "b's child"},
		{n: index.Node{NodeID: "A", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
	}, del(10, "A", "A", true))
	mustRead(t, filepath.Join(root, "A", "x.md"), "b's child")
	wantRow(t, e, "B", true)
	wantRow(t, e, "c", true)
	wantRow(t, e, "A", false)
	wantNoLocalChanges(t, e)
}

// R7-3 (the rename-over the other way): the live node, learned first, is deleted while a
// ghost learned later shares its place. The server no longer has the ghost: an ordinary
// delete, the ghost's row with it, and nothing left to re-upload.
func TestLegacyTieLiveDeleteWithGhostLearnedLater(t *testing.T) {
	e, root := serverFixture(t, server(), []row{
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 2}, content: "b"},
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("g")), Size: 1}},
	}, del(10, "b", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantClean(t, e)
}

// A ghost's purge where the server still has the rival: only the ghost's row goes.
func TestLegacyTieGhostPurgeKeepsLiveFile(t *testing.T) {
	e, root := serverFixture(t, server("b"), []row{
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("old")), Size: 3}},
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "live"},
	}, del(10, "g", "x.md", false))
	mustRead(t, filepath.Join(root, "x.md"), "live")
	wantRow(t, e, "g", false)
	wantRow(t, e, "b", true)
	wantNoLocalChanges(t, e)
}

// The live file's delete first (the ghost is gone on the server): both rows go; the
// ghost's later purge row is a no-op.
func TestLegacyTieLiveDeleteTakesFile(t *testing.T) {
	e, root := serverFixture(t, server(), []row{
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("old")), Size: 3}},
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "live"},
	}, del(10, "b", "x.md", false), del(11, "g", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantClean(t, e)
}

// A legacy ghost and a pushed live row: the ghost's purge keeps the pushed file.
func TestLegacyTiePushedFileKept(t *testing.T) {
	e, root := serverFixture(t, server("b"), []row{
		{n: index.Node{NodeID: "g", RelPath: "n.md", LocalPath: "n.md", Version: 1, ContentHash: hashOf([]byte("v1")), Size: 2}},
		{n: index.Node{NodeID: "b", RelPath: "n.md", LocalPath: "n.md", Version: 1}, content: "v2 edited+pushed"},
	}, del(10, "g", "n.md", false))
	mustRead(t, filepath.Join(root, "n.md"), "v2 edited+pushed")
	wantRow(t, e, "b", true)
	wantNoLocalChanges(t, e)
}

// R6-2: the disk holds the ghost's bytes (a local revert); the server has b, so b stays.
func TestLegacyTieRevertedEditKeepsLiveFile(t *testing.T) {
	srv := server("b")
	srv.changes = []Change{del(10, "g", "n.md", false)}
	e, root := indexRowsOn(t, srv, []row{
		{n: index.Node{NodeID: "g", RelPath: "n.md", LocalPath: "n.md", Version: 1, ContentHash: hashOf([]byte("v1")), Size: 2}},
		{n: index.Node{NodeID: "b", RelPath: "n.md", LocalPath: "n.md", Version: 2, ContentHash: hashOf([]byte("v2")), Size: 2}},
	})
	writeAt(t, filepath.Join(root, "n.md"), "v1")
	pullDeletes(t, e)
	mustRead(t, filepath.Join(root, "n.md"), "v1")
	wantRow(t, e, "b", true)
	wantRow(t, e, "g", false)
}

// R6-11: a legacy pair with equal content; the live file's delete takes the file.
func TestLegacyTieSameHashLiveDelete(t *testing.T) {
	e, root := serverFixture(t, server(), []row{
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "same"},
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 2}, content: "same"},
	}, del(10, "b", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantClean(t, e)
}

// R6-1 and folders: whichever row was learned first, the server's answer decides.
func TestLegacyTieFolder(t *testing.T) {
	for _, gFirst := range []bool{true, false} {
		rows := []row{
			{n: index.Node{NodeID: "g", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
			{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
			{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: "A/x.md", Version: 1}, content: "live child"},
		}
		if !gFirst {
			rows[0], rows[1] = rows[1], rows[0]
		}
		// The ghost's purge while b is live: only g's row.
		e, root := serverFixture(t, server("b", "c"), rows, del(10, "g", "A", true))
		mustRead(t, filepath.Join(root, "A", "x.md"), "live child")
		wantRow(t, e, "b", true)
		wantRow(t, e, "c", true)
		wantRow(t, e, "g", false)
		wantNoLocalChanges(t, e)
		// R6-1: only the live folder's delete comes (the ghost is gone): the tree goes.
		e, root = serverFixture(t, server(), rows, del(10, "b", "A", true))
		mustBeGone(t, filepath.Join(root, "A"))
		wantClean(t, e)
		// Both deletes, either order.
		for _, order := range [][2]string{{"g", "b"}, {"b", "g"}} {
			e, root := serverFixture(t, server(), rows, del(10, order[0], "A", true), del(11, order[1], "A", true))
			mustBeGone(t, filepath.Join(root, "A"))
			wantClean(t, e)
		}
	}
}

// A tie between rows at different local paths: the server's answer decides too.
func TestTieAtDifferentPlacesAskedOfServer(t *testing.T) {
	bl := localname.Disambiguate("x.md", "b")
	rows := []row{
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "old"},
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: bl, Version: 1}, content: "live"},
	}
	// The ghost's purge: its own file goes, b is untouched.
	e, root := serverFixture(t, server("b"), rows, del(10, "g", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	mustRead(t, filepath.Join(root, bl), "live")
	wantRow(t, e, "b", true)
	wantNoLocalChanges(t, e)
	// The live node's delete: the ghost is gone too, both go.
	e, root = serverFixture(t, server(), rows, del(10, "b", "x.md", false))
	mustBeGone(t, filepath.Join(root, bl))
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantClean(t, e)
}

// Without a way to ask the server, a tie is settled without guessing: only the deleted
// node's row goes, and the disk is left alone.
func TestTieWithoutServerCheckIsRowOnly(t *testing.T) {
	for _, first := range []string{"g", "b"} {
		e, root := deleteFixture(t, []row{
			{n: index.Node{NodeID: "g", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
			{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
			{n: index.Node{NodeID: "c", RelPath: "A/x.md", LocalPath: "A/x.md", Version: 1}, content: "child"},
		}, del(10, first, "A", true))
		mustRead(t, filepath.Join(root, "A", "x.md"), "child")
		wantRow(t, e, first, false)
		wantRow(t, e, "c", true)
		wantNoLocalChanges(t, e)
	}
}

// A check that fails (network, 5xx, certificate) is never guessed: the pull fails with the
// cursor held and nothing changed, and the retry decides.
func TestTieServerCheckFailureHoldsCursor(t *testing.T) {
	srv := server()
	srv.fail["g"] = errors.New("GET /files/g: 503")
	srv.changes = []Change{del(10, "b", "x.md", false), fileChange(11, "n", "new.md", "new")}
	srv.bodies = map[string][][]byte{"n": {[]byte("new")}}
	e, root := indexRowsOn(t, srv, []row{
		{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("g")), Size: 1}},
		{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 2}, content: "b"},
	})
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatal("want the check's error")
	}
	if c, _ := e.idx.Cursor(); c != 9 {
		t.Errorf("cursor = %d, want 9", c)
	}
	mustRead(t, filepath.Join(root, "x.md"), "b")
	wantRow(t, e, "b", true)
	wantRow(t, e, "g", true)
	delete(srv.fail, "g")
	srv.bodies = map[string][][]byte{"n": {[]byte("new")}}
	pullDeletes(t, e)
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantRow(t, e, "g", false)
	wantRow(t, e, "b", false)
	if c, _ := e.idx.Cursor(); c != 11 {
		t.Errorf("cursor = %d, want 11", c)
	}
}

// A strictly higher seq decides without asking; a tie asks once per rival per pull, however
// many deletes and descendants are involved.
func TestServerCheckOnlyOnTiesAndOncePerPull(t *testing.T) {
	srv := server("b")
	e, _ := serverFixture(t, srv, []row{
		{n: index.Node{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "b2", RelPath: "P", LocalPath: localname.Disambiguate("P", "b2"), IsDir: true, Version: 1}, seq: 5},
	}, del(10, "a", "P", true))
	if srv.totalCalls() != 0 {
		t.Errorf("calls %v, want none for a higher seq", srv.calls)
	}
	rows := []row{
		{n: index.Node{NodeID: "b", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
		{n: index.Node{NodeID: "g1", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
		{n: index.Node{NodeID: "g2", RelPath: "A", LocalPath: "A", IsDir: true, Version: 1}},
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("k%d", i)
		rows = append(rows, row{n: index.Node{NodeID: id, RelPath: "A/" + id + ".md", LocalPath: "A/" + id + ".md", Version: 1}, content: id})
	}
	srv = server("b")
	e, _ = serverFixture(t, srv, rows, del(10, "g1", "A", true), del(11, "g2", "A", true))
	wantRow(t, e, "b", true)
	wantRow(t, e, "k7", true)
	if srv.calls["b"] != 1 || srv.totalCalls() > 3 {
		t.Errorf("calls %v, want b asked once and nothing per descendant", srv.calls)
	}
}

// Round 9.

// httpStatusErr is a server answer with an HTTP status, as protocol.StatusError reports it.
type httpStatusErr int

func (e httpStatusErr) Error() string   { return fmt.Sprintf("GET /files: %d", int(e)) }
func (e httpStatusErr) HTTPStatus() int { return int(e) }

// m-2 (probe MixedTie): three legacy rows at one server path. The rival sharing n's place is
// gone on the server too; the live one is elsewhere. The place holds nothing live: n's and
// the gone rival's rows go, and so does the file (it holds their synced content).
func TestMixedTieGoneRivalAtPlaceGoes(t *testing.T) {
	r2l := localname.Disambiguate("x.md", "r2")
	e, root := serverFixture(t, server("r2"), []row{
		{n: index.Node{NodeID: "n", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "old"},
		{n: index.Node{NodeID: "r1", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "old"},
		{n: index.Node{NodeID: "r2", RelPath: "x.md", LocalPath: r2l, Version: 1}, content: "live"},
	}, del(10, "n", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	mustRead(t, filepath.Join(root, r2l), "live")
	wantRow(t, e, "n", false)
	wantRow(t, e, "r1", false)
	wantRow(t, e, "r2", true)
	wantNoLocalChanges(t, e)
}

// The same with the rival at n's place live: the place is its, only n's row goes.
func TestMixedTieLiveRivalAtPlaceKeepsIt(t *testing.T) {
	r2l := localname.Disambiguate("x.md", "r2")
	e, root := serverFixture(t, server("r1", "r2"), []row{
		{n: index.Node{NodeID: "n", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("n")), Size: 1}},
		{n: index.Node{NodeID: "r1", RelPath: "x.md", LocalPath: "x.md", Version: 1}, content: "r1"},
		{n: index.Node{NodeID: "r2", RelPath: "x.md", LocalPath: r2l, Version: 1}, content: "live"},
	}, del(10, "n", "x.md", false))
	mustRead(t, filepath.Join(root, "x.md"), "r1")
	wantRow(t, e, "n", false)
	wantRow(t, e, "r1", true)
	wantRow(t, e, "r2", true)
}

// The live rival is decided by a higher seq, the rival at n's place is a tie: it is asked,
// and when gone, the place is cleared like an ordinary delete.
func TestHigherSeqLiveTiedGoneRivalAtPlace(t *testing.T) {
	r2l := localname.Disambiguate("x.md", "r2")
	srv := server()
	e, root := serverFixture(t, srv, []row{
		{n: index.Node{NodeID: "n", RelPath: "x.md", LocalPath: "x.md", Version: 1}, seq: 2, content: "old"},
		{n: index.Node{NodeID: "r1", RelPath: "x.md", LocalPath: "x.md", Version: 1}, seq: 2, content: "old"},
		{n: index.Node{NodeID: "r2", RelPath: "x.md", LocalPath: r2l, Version: 1}, seq: 5, content: "live"},
	}, del(10, "n", "x.md", false))
	mustBeGone(t, filepath.Join(root, "x.md"))
	wantRow(t, e, "r1", false)
	wantRow(t, e, "r2", true)
	if srv.calls["r2"] != 0 {
		t.Errorf("the higher-seq rival was asked: %v", srv.calls)
	}
}

// m-1: an answer that is not "not found" and not a temporary failure (a proxy or WAF
// refusing GET /files/{id}, a 400) cannot hold the whole feed: that change is settled
// without guessing (only the deleted node's row goes, the disk stays) and the pull goes on.
func TestTieServerCheckRefusedIsRowOnly(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		srv := server()
		srv.fail["g"] = httpStatusErr(code)
		srv.changes = []Change{del(10, "b", "x.md", false), fileChange(11, "n", "new.md", "new")}
		srv.bodies = map[string][][]byte{"n": {[]byte("new")}}
		e, root := indexRowsOn(t, srv, []row{
			{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("g")), Size: 1}},
			{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 2}, content: "b"},
		})
		pullDeletes(t, e)
		if c, _ := e.idx.Cursor(); c != 11 {
			t.Errorf("%d: cursor = %d, want 11", code, c)
		}
		mustRead(t, filepath.Join(root, "x.md"), "b")
		mustRead(t, filepath.Join(root, "new.md"), "new")
		wantRow(t, e, "b", false)
		wantRow(t, e, "g", true)
	}
}

// Temporary failures (5xx, 429) and transport errors still hold the cursor.
func TestTieServerCheckTemporaryFailureHoldsCursor(t *testing.T) {
	for _, fail := range []error{httpStatusErr(500), httpStatusErr(503), httpStatusErr(429), errors.New("dial tcp: connection refused")} {
		srv := server()
		srv.fail["g"] = fail
		srv.changes = []Change{del(10, "b", "x.md", false)}
		e, root := indexRowsOn(t, srv, []row{
			{n: index.Node{NodeID: "g", RelPath: "x.md", LocalPath: "x.md", Version: 1, ContentHash: hashOf([]byte("g")), Size: 1}},
			{n: index.Node{NodeID: "b", RelPath: "x.md", LocalPath: "x.md", Version: 2}, content: "b"},
		})
		if err := e.PullOnce(context.Background()); err == nil {
			t.Errorf("%v: want the pull to fail", fail)
		}
		if c, _ := e.idx.Cursor(); c != 9 {
			t.Errorf("%v: cursor = %d, want 9", fail, c)
		}
		mustRead(t, filepath.Join(root, "x.md"), "b")
		wantRow(t, e, "b", true)
	}
}
