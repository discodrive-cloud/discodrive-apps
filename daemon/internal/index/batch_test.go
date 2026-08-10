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
