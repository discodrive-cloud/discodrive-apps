package index

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// An index written before the seq column existed opens, gains the column with 0 for every
// row, and records seqs from then on.
func TestOldIndexWithoutSeqMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE nodes (
		node_id TEXT PRIMARY KEY, rel_path TEXT NOT NULL, is_dir INTEGER NOT NULL,
		version INTEGER NOT NULL, content_hash TEXT, size INTEGER,
		local_path TEXT NOT NULL DEFAULT '');
		INSERT INTO nodes(node_id, rel_path, is_dir, version, content_hash, size) VALUES('d', 'Docs', 1, 1, '', 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	idx, err := Open(path)
	if err != nil {
		t.Fatalf("Open old index: %v", err)
	}
	defer idx.Close()
	if n, ok, err := idx.Get("d"); err != nil || !ok || n.RelPath != "Docs" || !n.IsDir {
		t.Fatalf("old row after migration: %+v ok=%v err=%v", n, ok, err)
	}
	seqs, err := idx.DirSeqs()
	if err != nil || seqs["d"] != 0 {
		t.Fatalf("DirSeqs = %v, %v; want d:0", seqs, err)
	}
	if err := idx.SetSeq("d", 7); err != nil {
		t.Fatal(err)
	}
	// Put keeps the recorded seq.
	if err := idx.Put(Node{NodeID: "d", RelPath: "Docs", IsDir: true, Version: 2}); err != nil {
		t.Fatal(err)
	}
	if seqs, _ := idx.DirSeqs(); seqs["d"] != 7 {
		t.Fatalf("seq after Put = %d, want 7", seqs["d"])
	}
	idx.Close()
	// Reopening an already migrated index is fine too.
	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	again.Close()
}
