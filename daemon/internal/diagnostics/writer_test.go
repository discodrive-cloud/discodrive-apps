package diagnostics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOptInRotationAndDisable(t *testing.T) {
	w := &Writer{Limit: 8}
	p := filepath.Join(t.TempDir(), "Logs", "sync.log")
	w.Write([]byte("off"))
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("disabled writer created a file")
	}
	if err := w.Configure(p); err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("first\n"))
	w.Write([]byte("second\n"))
	if b, _ := os.ReadFile(p + ".1"); string(b) != "first\n" {
		t.Fatalf("backup %q", b)
	}
	if err := w.Configure(""); err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("off again"))
	if b, _ := os.ReadFile(p); string(b) != "second\n" {
		t.Fatalf("disabled writer appended %q", b)
	}
}
