package index

import (
	"path/filepath"
	"testing"
)

// A node's name on disk can differ from its name on the server, because some server names
// contain characters the filesystem rejects. The index has to remember both, or the push side
// — which matches files on disk against the index — would see a rename that never happened.

func TestNodeRemembersLocalPath(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	want := Node{NodeID: "n1", RelPath: "notes/why?.md", LocalPath: "notes/why？.md", Version: 1}
	if err := idx.Put(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := idx.Get("n1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.LocalPath != want.LocalPath || got.RelPath != want.RelPath {
		t.Errorf("got rel=%q local=%q", got.RelPath, got.LocalPath)
	}

	all, err := idx.All()
	if err != nil || len(all) != 1 || all[0].LocalPath != want.LocalPath {
		t.Errorf("All: %+v err=%v", all, err)
	}
}

// Rows written before the column existed, and every node whose name needs no changing, carry
// an empty local path. Reads must report it as equal to the server path so callers have one
// rule rather than two.
func TestLocalPathDefaultsToRelPath(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.Put(Node{NodeID: "n1", RelPath: "notes/plain.md", Version: 1}); err != nil {
		t.Fatal(err)
	}
	got, _, err := idx.Get("n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalPath != "notes/plain.md" {
		t.Errorf("LocalPath = %q, want it to fall back to RelPath", got.LocalPath)
	}
}

// Used to settle the rare case of two server names mapping to one local name.
func TestNodeIDByLocalPath(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	if err := idx.Put(Node{NodeID: "n1", RelPath: "a?.md", LocalPath: "a？.md", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(Node{NodeID: "n2", RelPath: "plain.md", Version: 1}); err != nil {
		t.Fatal(err)
	}

	id, ok, err := idx.NodeIDByLocalPath("a？.md")
	if err != nil || !ok || id != "n1" {
		t.Errorf("localized lookup: id=%q ok=%v err=%v", id, ok, err)
	}
	// Falls back to rel_path for rows that store no local path of their own.
	id, ok, err = idx.NodeIDByLocalPath("plain.md")
	if err != nil || !ok || id != "n2" {
		t.Errorf("fallback lookup: id=%q ok=%v err=%v", id, ok, err)
	}
	if _, ok, _ = idx.NodeIDByLocalPath("nothing.md"); ok {
		t.Error("unknown path must not resolve")
	}
}
