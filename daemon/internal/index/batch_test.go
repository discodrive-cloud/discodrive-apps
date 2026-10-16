package index

import (
	"errors"
	"path/filepath"
	"testing"
)

// The first pull after pairing applies the whole tree. Doing that a row at a time means one
// transaction — and on a phone one fsync — per row, which is what left a freshly paired app
// staring at an empty list for minutes. A page has to go in as one transaction.

func TestOpenUsesWALAndRelaxedSync(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	var mode string
	if err := idx.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var sync int
	if err := idx.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if sync != 1 { // NORMAL: durable enough for a rebuildable cache, without a fsync per commit
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}
}

func TestBatchAppliesInOrderAndCommitsOnce(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	err = idx.Batch(func(b *Batch) error {
		if err := b.Put(Node{NodeID: "n1", RelPath: "a.txt", Version: 1}); err != nil {
			return err
		}
		if err := b.Put(Node{NodeID: "n2", RelPath: "b.txt", Version: 1}); err != nil {
			return err
		}
		// Same page can create and then remove a node — order has to be preserved.
		if err := b.Delete("n1"); err != nil {
			return err
		}
		return b.SetCursor(7)
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}

	if _, ok, _ := idx.Get("n1"); ok {
		t.Error("n1 was deleted later in the batch, it must be gone")
	}
	if _, ok, _ := idx.Get("n2"); !ok {
		t.Error("n2 missing")
	}
	if c, _ := idx.Cursor(); c != 7 {
		t.Errorf("cursor = %d, want 7", c)
	}
}

// A page that fails midway must leave nothing behind — otherwise the cursor could advance
// past rows that were never applied, and they would never be pulled again.
func TestBatchRollsBackOnError(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()

	boom := errors.New("boom")
	err = idx.Batch(func(b *Batch) error {
		if err := b.Put(Node{NodeID: "n1", RelPath: "a.txt", Version: 1}); err != nil {
			return err
		}
		if err := b.SetCursor(9); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Batch err = %v, want boom", err)
	}
	if _, ok, _ := idx.Get("n1"); ok {
		t.Error("n1 must not survive a rolled-back batch")
	}
	if c, _ := idx.Cursor(); c != 0 {
		t.Errorf("cursor = %d, want 0 — a rolled-back page must not advance it", c)
	}
}

// The change feed deletes a folder with one event for the folder alone. Applied by node id
// only, everything below it stayed in the index: a re-created folder of the same name then
// resolved its children to the dead node ids, and uploads into them failed with 404.
func TestBatchDeleteSubtreeTakesTheFolderContents(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	for _, n := range []Node{
		{NodeID: "up", RelPath: "Uploads", IsDir: true, Version: 1},
		{NodeID: "dev", RelPath: "Uploads/Pixel", IsDir: true, Version: 1},
		{NodeID: "img", RelPath: "Uploads/Pixel/a.jpg", Version: 1},
		{NodeID: "top", RelPath: "Uploads/b.jpg", Version: 1},
		// Neighbours that only look related.
		{NodeID: "sib", RelPath: "Uploads2/c.jpg", Version: 1},
		{NodeID: "case", RelPath: "uploads/d.jpg", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Batch(func(b *Batch) error { return b.DeleteSubtree("up") }); err != nil {
		t.Fatalf("Batch: %v", err)
	}
	for _, id := range []string{"up", "dev", "img", "top"} {
		if _, ok, _ := idx.Get(id); ok {
			t.Errorf("%s is under the deleted folder, it must be gone", id)
		}
	}
	for _, id := range []string{"sib", "case"} {
		if _, ok, _ := idx.Get(id); !ok {
			t.Errorf("%s is not under the deleted folder, it must stay", id)
		}
	}
	// Unknown ids are a no-op: the feed can delete a node this index never saw.
	if err := idx.Batch(func(b *Batch) error { return b.DeleteSubtree("nope") }); err != nil {
		t.Errorf("unknown node: %v", err)
	}
}

// A ghost (a second node recorded at the same path) must not take the live node's tree
// with it — the rule SubtreeOf already follows.
func TestBatchDeleteSubtreeOfSharedPathIsTheNodeOnly(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer idx.Close()
	for _, n := range []Node{
		{NodeID: "ghost", RelPath: "Docs", IsDir: true, Version: 1},
		{NodeID: "live", RelPath: "Docs", IsDir: true, Version: 2},
		{NodeID: "f", RelPath: "Docs/x.txt", Version: 1},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Batch(func(b *Batch) error { return b.DeleteSubtree("ghost") }); err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if _, ok, _ := idx.Get("ghost"); ok {
		t.Error("ghost must be gone")
	}
	for _, id := range []string{"live", "f"} {
		if _, ok, _ := idx.Get(id); !ok {
			t.Errorf("%s belongs to the live folder, it must stay", id)
		}
	}
}
