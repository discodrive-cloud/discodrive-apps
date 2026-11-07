package index

import (
	"path/filepath"
	"testing"
)

// Android has no /tmp and no TMPDIR: an SQLite operation that needs a temporary file
// failed with "disk I/O error (6410)" (SQLITE_IOERR_GETTEMPPATH). Temporary tables and
// sorts must stay in memory.
func TestOpenKeepsTemporaryDataInMemory(t *testing.T) {
	ix, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var mode int
	if err := ix.db.QueryRow("PRAGMA temp_store").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != 2 {
		t.Fatalf("temp_store = %d, want 2 (MEMORY)", mode)
	}
}
