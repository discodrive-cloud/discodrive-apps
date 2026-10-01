package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"discodrive.org/daemon/internal/engine"
)

func readQuarantine(t *testing.T, path string) string {
	t.Helper()
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, quarantineXattr, buf)
	if err != nil {
		t.Fatalf("read %s: %v", quarantineXattr, err)
	}
	return string(buf[:n])
}

func TestMarkDownloadedSetsQuarantine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.pdf.js")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	markDownloaded(p)
	got := readQuarantine(t, p)
	if !strings.HasPrefix(got, "0083;") || !strings.HasSuffix(got, ";DiscoDrive;") {
		t.Fatalf("quarantine = %q", got)
	}
}

// A file opened from the browser is fetched through Controller.Open; the cached copy
// must carry the quarantine mark.
func TestFetchQuarantinesDownloadedFile(t *testing.T) {
	c, srv := newQuarantineFixture(t)
	p, err := c.Open(t.Context(), srv)
	if err != nil {
		t.Fatal(err)
	}
	if got := readQuarantine(t, p); !strings.Contains(got, "DiscoDrive") {
		t.Fatalf("quarantine = %q", got)
	}
}

func newQuarantineFixture(t *testing.T) (*Controller, string) {
	t.Helper()
	f := &fakeServer{
		pages: []fakePage{{changes: []engine.Change{
			{Seq: 1, Op: "upsert", NodeID: "a", RelPath: "report.pdf.js", Version: 1, Size: 1},
		}, next: 1}},
		download: map[string][]byte{"a": []byte("x")},
	}
	c, _ := newTestController(t, f)
	if _, err := c.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	return c, "a"
}
