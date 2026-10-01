package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
)

// A ghost folder (purged on the server, its delete delivered only later) keeps its path in the
// index while a live folder takes the same server path. A file created in the live folder
// can be placed inside the ghost's local folder, and the ghost's late delete must not take
// that file (or its index entry) with it.
func TestLateGhostFolderDeleteKeepsLiveFileAtItsPath(t *testing.T) {
	x := []byte("live")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "b", RelPath: "Q", IsDir: true, Version: 1},
			{Seq: 2, NodeID: "a", RelPath: "P", IsDir: true, Version: 1},
		},
		bodies: map[string][][]byte{"c": {x}},
	}
	e, root := newEngine(t, src)
	pull := func() {
		t.Helper()
		if err := e.PullOnce(context.Background()); err != nil {
			t.Fatalf("PullOnce: %v", err)
		}
	}
	pull()
	// a is purged on the server (its delete comes later); b moves into its path.
	src.changes = append(src.changes, Change{Seq: 3, NodeID: "b", RelPath: "P", IsDir: true, Version: 1})
	pull()
	src.changes = append(src.changes,
		Change{Seq: 4, NodeID: "c", RelPath: "P/x.md", Version: 1, ContentHash: hashOf(x), Size: int64(len(x))},
		Change{Seq: 5, NodeID: "a", RelPath: "P", IsDir: true, Deleted: true})
	pull()

	c, ok, err := e.idx.Get("c")
	if err != nil || !ok {
		t.Fatalf("live file c forgotten by the ghost's delete: ok=%v err=%v", ok, err)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(c.LocalPath)), "live")
	if _, ok, _ := e.idx.Get("a"); ok {
		t.Fatal("ghost a still indexed")
	}
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// The live folder can also hold the plain name while the ghost holds a generated one: the
// ghost got it for a case clash with a third folder that has moved away since. The folder
// the feed placed at the path last is the live one, whatever the names.
func TestLateGhostFolderDeleteKeepsLiveFileWhenGhostHasTheGeneratedName(t *testing.T) {
	x := []byte("live")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d", RelPath: "p", IsDir: true, Version: 1},
			{Seq: 2, NodeID: "b", RelPath: "B", IsDir: true, Version: 1},
			{Seq: 3, NodeID: "a", RelPath: "P", IsDir: true, Version: 1},
		},
		bodies: map[string][][]byte{"c": {x}},
	}
	e, root := newEngine(t, src)
	pull := func() {
		t.Helper()
		if err := e.PullOnce(context.Background()); err != nil {
			t.Fatalf("PullOnce: %v", err)
		}
	}
	pull()
	if a, _, _ := e.idx.Get("a"); a.LocalPath != localname.Disambiguate("P", "a") {
		t.Fatalf("a placed at %q, want the generated name", a.LocalPath)
	}
	// a is purged on the server (its delete comes later); d moves away, b takes P.
	src.changes = append(src.changes,
		Change{Seq: 4, NodeID: "d", RelPath: "r", IsDir: true, Version: 1},
		Change{Seq: 5, NodeID: "b", RelPath: "P", IsDir: true, Version: 1})
	pull()
	if b, _, _ := e.idx.Get("b"); b.LocalPath != "P" {
		t.Fatalf("b placed at %q, want the plain name", b.LocalPath)
	}
	src.changes = append(src.changes,
		Change{Seq: 6, NodeID: "c", RelPath: "P/x.md", Version: 1, ContentHash: hashOf(x), Size: int64(len(x))},
		Change{Seq: 7, NodeID: "a", RelPath: "P", IsDir: true, Deleted: true})
	pull()

	c, ok, err := e.idx.Get("c")
	if err != nil || !ok {
		t.Fatalf("live file c forgotten by the ghost's delete: ok=%v err=%v", ok, err)
	}
	if c.LocalPath != "P/x.md" {
		t.Errorf("c placed at %q, want it in the live folder", c.LocalPath)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(c.LocalPath)), "live")
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// The name generated for a clashing newcomer can already belong to another node, one whose
// server name is exactly that generated name and which arrived first. The newcomer must
// get a name nobody holds instead of landing on that node's file.
func TestGeneratedLocalNameAlreadyHeld(t *testing.T) {
	upper, lower, third := []byte("upper"), []byte("lower"), []byte("third")
	generated := localname.Disambiguate("foo.md", "n2")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n3", RelPath: generated, Version: 1, ContentHash: hashOf(third), Size: int64(len(third))},
			{Seq: 2, NodeID: "n1", RelPath: "Foo.md", Version: 1, ContentHash: hashOf(upper), Size: int64(len(upper))},
			{Seq: 3, NodeID: "n2", RelPath: "foo.md", Version: 1, ContentHash: hashOf(lower), Size: int64(len(lower))},
		},
		bodies: map[string][][]byte{"n1": {upper}, "n2": {lower}, "n3": {third}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	n2, _, _ := e.idx.Get("n2")
	n3, _, _ := e.idx.Get("n3")
	if n2.LocalPath == n3.LocalPath {
		t.Fatalf("n2 placed on n3's file %q", n2.LocalPath)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(n2.LocalPath)), "lower")
	mustRead(t, filepath.Join(root, filepath.FromSlash(n3.LocalPath)), "third")
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
	// Deterministic: the same feed on another device gives the same name.
	src2 := &fakeSource{changes: src.changes,
		bodies: map[string][][]byte{"n1": {upper}, "n2": {lower}, "n3": {third}}}
	e2, _ := newEngine(t, src2)
	if err := e2.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if again, _, _ := e2.idx.Get("n2"); again.LocalPath != n2.LocalPath {
		t.Fatalf("n2 placed at %q, then %q", n2.LocalPath, again.LocalPath)
	}
}

// Rows from before seqs were recorded all have 0. On that tie the folder whose local name
// was generated from the other's is the newcomer, and children go into it, whatever the
// row order.
func TestSharedPathTieFallsBackToGeneratedName(t *testing.T) {
	x := []byte("live")
	src := &fakeSource{
		changes: []Change{{Seq: 10, NodeID: "c", RelPath: "P/x.md", Version: 1, ContentHash: hashOf(x), Size: int64(len(x))}},
		bodies:  map[string][][]byte{"c": {x}},
	}
	e, root := newEngine(t, src)
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	if err := e.idx.SetCursor(9); err != nil {
		t.Fatal(err)
	}
	live := localname.Disambiguate("P", "b")
	// The live folder's row first, so without the tie rule the ghost would win as the last.
	for _, n := range []index.Node{
		{NodeID: "b", RelPath: "P", LocalPath: live, IsDir: true, Version: 1},
		{NodeID: "a", RelPath: "P", LocalPath: "P", IsDir: true, Version: 1},
	} {
		if err := e.idx.Put(n); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, n.LocalPath), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	c, _, _ := e.idx.Get("c")
	if c.LocalPath != live+"/x.md" {
		t.Fatalf("c placed at %q, want it in the live folder %q", c.LocalPath, live)
	}
}
