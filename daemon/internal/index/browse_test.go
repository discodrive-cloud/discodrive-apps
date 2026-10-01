package index

import (
	"path/filepath"
	"sort"
	"testing"
)

func names(ns []Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.RelPath
	}
	sort.Strings(out)
	return out
}

func TestChildren(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	for _, n := range []Node{
		{NodeID: "d1", RelPath: "docs", IsDir: true, Version: 1},
		{NodeID: "f1", RelPath: "docs/a.txt", Version: 1, Size: 3},
		{NodeID: "f2", RelPath: "docs/b.txt", Version: 1, Size: 4},
		{NodeID: "sub", RelPath: "docs/sub", IsDir: true, Version: 1},
		{NodeID: "deep", RelPath: "docs/sub/c.txt", Version: 1},
		{NodeID: "top", RelPath: "top.txt", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}

	root, _ := idx.Children("")
	if got := names(root); len(got) != 2 || got[0] != "docs" || got[1] != "top.txt" {
		t.Fatalf("root children = %v, want [docs top.txt]", got)
	}
	docs, _ := idx.Children("docs")
	if got := names(docs); len(got) != 3 || got[0] != "docs/a.txt" || got[1] != "docs/b.txt" || got[2] != "docs/sub" {
		t.Fatalf("docs children = %v, want [docs/a.txt docs/b.txt docs/sub] (NOT docs/sub/c.txt)", got)
	}
}

func TestLocalTable(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if err := idx.SetLocal("f1", "pinned", 1, "/p/docs/a.txt"); err != nil {
		t.Fatal(err)
	}
	state, stale, path := idx.LocalStatus("f1", 1)
	if state != "pinned" || stale || path != "/p/docs/a.txt" {
		t.Fatalf("local: %s %v %s", state, stale, path)
	}
	if _, st, _ := idx.LocalStatus("f1", 2); !st {
		t.Fatal("serverVersion 2 > 1 must be stale")
	}
	if idx.LocalPathOf("f1") != "/p/docs/a.txt" {
		t.Fatal("LocalPathOf mismatch")
	}
	if pins := idx.ListPinned(); len(pins) != 1 || pins[0] != "f1" {
		t.Fatalf("pinned: %v", pins)
	}
	if err := idx.DeleteLocal("f1"); err != nil {
		t.Fatal(err)
	}
	if state, _, _ := idx.LocalStatus("f1", 1); state != "" {
		t.Fatalf("after delete state=%q", state)
	}
}

// Subtree is what a client forgets when the server says a node is gone: the node itself
// and everything under it, never a sibling whose name merely starts the same way.
func TestSubtree(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	for _, n := range []Node{
		{NodeID: "d1", RelPath: "new1", IsDir: true, Version: 1},
		{NodeID: "app", RelPath: "new1/DiscoDrive.app", IsDir: true, Version: 1},
		{NodeID: "plist", RelPath: "new1/DiscoDrive.app/Info.plist", Version: 1},
		{NodeID: "sib", RelPath: "new1/DiscoDrive.app.zip", Version: 1},
		{NodeID: "pct", RelPath: "new1/a%b", Version: 1},
		{NodeID: "top", RelPath: "new10", IsDir: true, Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := idx.Subtree("new1/DiscoDrive.app")
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); len(n) != 2 || n[0] != "new1/DiscoDrive.app" || n[1] != "new1/DiscoDrive.app/Info.plist" {
		t.Fatalf("Subtree(app) = %v", n)
	}
	got, _ = idx.Subtree("new1")
	if len(got) != 5 {
		t.Fatalf("Subtree(new1) = %v, want 5 nodes (not new10)", names(got))
	}
	got, _ = idx.Subtree("new1/a%b")
	if len(got) != 1 {
		t.Fatalf("Subtree(a%%b) = %v, want only itself", names(got))
	}
	// The server allows "Docs" and "docs" as siblings: the subtree match is case-sensitive.
	for _, n := range []Node{
		{NodeID: "D", RelPath: "Docs", IsDir: true, Version: 1},
		{NodeID: "Da", RelPath: "Docs/a", Version: 1},
		{NodeID: "d", RelPath: "docs", IsDir: true, Version: 1},
		{NodeID: "db", RelPath: "docs/b", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = idx.Subtree("Docs")
	if n := names(got); len(n) != 2 || n[0] != "Docs" || n[1] != "Docs/a" {
		t.Fatalf("Subtree(Docs) = %v, want [Docs Docs/a] only", n)
	}
}

// "Docs" and "docs" are distinct siblings on the server; each lists only its own children.
func TestChildrenIsCaseSensitive(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	for _, n := range []Node{
		{NodeID: "D", RelPath: "Docs", IsDir: true, Version: 1},
		{NodeID: "Da", RelPath: "Docs/a", Version: 1},
		{NodeID: "d", RelPath: "docs", IsDir: true, Version: 1},
		{NodeID: "db", RelPath: "docs/b", Version: 1},
		{NodeID: "dsub", RelPath: "docs/sub", IsDir: true, Version: 1},
		{NodeID: "deep", RelPath: "docs/sub/c", Version: 1},
		{NodeID: "pct", RelPath: "a%b", IsDir: true, Version: 1},
		{NodeID: "pctc", RelPath: "a%b/x", Version: 1},
		{NodeID: "axb", RelPath: "axb/y", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	for parent, want := range map[string][]string{
		"Docs": {"Docs/a"},
		"docs": {"docs/b", "docs/sub"},
		"a%b":  {"a%b/x"},
	} {
		got, err := idx.Children(parent)
		if err != nil {
			t.Fatal(err)
		}
		if n := names(got); len(n) != len(want) || (len(n) > 0 && (n[0] != want[0] || n[len(n)-1] != want[len(want)-1])) {
			t.Errorf("Children(%q) = %v, want %v", parent, n, want)
		}
	}
}

// SubtreeOf is the subtree to remove for one node. Paths are not unique: a ghost A can still
// be indexed at P after a live B was created there. Their children cannot be told apart by
// path (the index has no parent ids), so when another node shares the root's path only the
// node itself is returned.
func TestSubtreeOfSharedPathIsTheNodeOnly(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	for _, n := range []Node{
		{NodeID: "A", RelPath: "P", IsDir: true, Version: 1},
		{NodeID: "Ax", RelPath: "P/x", Version: 1},
		{NodeID: "B", RelPath: "P", IsDir: true, Version: 1},
		{NodeID: "By", RelPath: "P/y", Version: 1},
		{NodeID: "Q", RelPath: "Q", IsDir: true, Version: 1},
		{NodeID: "Qz", RelPath: "Q/z", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := idx.SubtreeOf("A")
	if err != nil {
		t.Fatal(err)
	}
	if n := names(got); len(n) != 1 || got[0].NodeID != "A" {
		t.Fatalf("SubtreeOf(A) = %v, want A only", n)
	}
	got, _ = idx.SubtreeOf("Q")
	if len(got) != 2 {
		t.Fatalf("SubtreeOf(Q) = %v, want Q and Q/z", names(got))
	}
	if got, _ = idx.SubtreeOf("unknown"); len(got) != 0 {
		t.Fatalf("SubtreeOf(unknown) = %v, want nothing", names(got))
	}
}

// Two nodes can hold the same local copy path (a ghost file and a live file of the same
// name). A copy another node still records is not the forgotten node's to delete.
func TestLocalPathShared(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	idx.SetLocal("A", "cached", 1, "/c/f.txt")
	idx.SetLocal("B", "pinned", 1, "/c/f.txt")
	idx.SetLocal("C", "cached", 1, "/c/g.txt")
	if !idx.LocalPathShared("A") || idx.LocalPathShared("C") || idx.LocalPathShared("none") {
		t.Fatal("LocalPathShared: want A shared, C and none not")
	}
}
