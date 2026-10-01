package index

import (
	"path/filepath"
	"testing"
)

// The index as it stood before a reset is kept on disk until the reset completes, so a
// restart in between still knows which files were synced.
func TestClearKeepingSnapshotMovesNodesAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	idx, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Node{NodeID: "n1", RelPath: "a:b.txt", LocalPath: "a~b.txt", Version: 3, ContentHash: "h1", Size: 7}
	if err := idx.Put(want); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(Node{NodeID: "d1", RelPath: "dir", IsDir: true, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetCursor(9); err != nil {
		t.Fatal(err)
	}
	if err := idx.ClearKeepingSnapshot(); err != nil {
		t.Fatalf("ClearKeepingSnapshot: %v", err)
	}
	if all, _ := idx.All(); len(all) != 0 {
		t.Fatalf("nodes after clear = %v, want none", all)
	}
	if c, _ := idx.Cursor(); c != 0 {
		t.Fatalf("cursor after clear = %d, want 0", c)
	}
	if ready, _ := idx.MirrorReady(); ready {
		t.Fatal("mirror still ready after clear")
	}
	idx.Close()

	idx, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	before, err := idx.BeforeReset()
	if err != nil {
		t.Fatalf("BeforeReset: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("snapshot = %v, want 2 nodes", before)
	}
	for _, n := range before {
		if n.NodeID == "n1" && n != want {
			t.Fatalf("snapshot node = %+v, want %+v", n, want)
		}
		if n.NodeID == "d1" && (!n.IsDir || n.LocalPath != "dir") {
			t.Fatalf("snapshot dir = %+v", n)
		}
	}

	if err := idx.ClearBeforeReset(); err != nil {
		t.Fatalf("ClearBeforeReset: %v", err)
	}
	if before, _ := idx.BeforeReset(); len(before) != 0 {
		t.Fatalf("snapshot after ClearBeforeReset = %v, want none", before)
	}
}

// A second clear before the reset completed must not replace the snapshot: the live
// nodes at that point are a partial re-pull, not what the folder held as synced.
func TestClearKeepingSnapshotFirstSnapshotWins(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.Put(Node{NodeID: "old", RelPath: "old.txt", Version: 1, ContentHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := idx.ClearKeepingSnapshot(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(Node{NodeID: "partial", RelPath: "partial.txt", Version: 1, ContentHash: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := idx.ClearKeepingSnapshot(); err != nil {
		t.Fatal(err)
	}
	if all, _ := idx.All(); len(all) != 0 {
		t.Fatalf("nodes after second clear = %v, want none", all)
	}
	before, _ := idx.BeforeReset()
	if len(before) != 1 || before[0].NodeID != "old" {
		t.Fatalf("snapshot = %v, want only the first one (old)", before)
	}

	// Once the snapshot is cleared, the next reset takes a fresh one.
	if err := idx.ClearBeforeReset(); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(Node{NodeID: "new", RelPath: "new.txt", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.ClearKeepingSnapshot(); err != nil {
		t.Fatal(err)
	}
	before, _ = idx.BeforeReset()
	if len(before) != 1 || before[0].NodeID != "new" {
		t.Fatalf("snapshot = %v, want [new]", before)
	}
}

// A plain Clear (re-pairing) leaves no snapshot behind.
func TestPlainClearTakesNoSnapshot(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.Put(Node{NodeID: "n", RelPath: "n.txt", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Clear(); err != nil {
		t.Fatal(err)
	}
	if before, _ := idx.BeforeReset(); len(before) != 0 {
		t.Fatalf("snapshot after Clear = %v, want none", before)
	}
}

// A new pairing starts from nothing: a snapshot from the old pairing's unfinished reset
// must not be applied to the new mirror by a later scope reset.
func TestBindMirrorPairingDropsTheSnapshot(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	if err := idx.BindMirrorPairing("old-pairing"); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(Node{NodeID: "n", RelPath: "n.txt", Version: 1, ContentHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := idx.ClearKeepingSnapshot(); err != nil {
		t.Fatal(err)
	}
	if before, _ := idx.BeforeReset(); len(before) != 1 {
		t.Fatalf("setup: snapshot = %v, want 1 node", before)
	}
	if err := idx.BindMirrorPairing("new-pairing"); err != nil {
		t.Fatal(err)
	}
	if before, err := idx.BeforeReset(); err != nil || len(before) != 0 {
		t.Fatalf("snapshot after a new pairing = %v, %v; want none", before, err)
	}
}
