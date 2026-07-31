package desktop

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMutationsPassThrough(t *testing.T) {
	f := &fakeServer{}
	c, _ := newTestController(t, f)
	ctx := context.Background()

	if err := c.CreateFolder(ctx, "p", "new"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := c.Rename(ctx, "n", "renamed"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := c.Move(ctx, "n", "p2"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := c.Delete(ctx, "n"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := c.Upload(ctx, "p", "up.txt", strings.NewReader("data"), time.Time{}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if len(f.created) != 1 || f.created[0] != [2]string{"p", "new"} {
		t.Fatalf("created = %v", f.created)
	}
	if len(f.renamed) != 1 || f.renamed[0] != [2]string{"n", "renamed"} {
		t.Fatalf("renamed = %v", f.renamed)
	}
	if len(f.moved) != 1 || f.moved[0] != [2]string{"n", "p2"} {
		t.Fatalf("moved = %v", f.moved)
	}
	if len(f.deleted) != 1 || f.deleted[0] != "n" {
		t.Fatalf("deleted = %v", f.deleted)
	}
	if len(f.uploaded) != 1 || f.uploaded[0] != "up.txt" {
		t.Fatalf("uploaded = %v", f.uploaded)
	}
}
