package engine

import (
	"context"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
	"os"
	"path/filepath"
	"testing"
)

func TestUnrelatedDirectoryKeepsLiteralName(t *testing.T) {
	localname.RestrictForTest(t)
	server := "old/report?.md"
	local := localname.Localize(server)
	e, root := indexRows(t, []row{{n: index.Node{NodeID: "deleted-file", RelPath: server, LocalPath: local, Version: 1, ContentHash: hashOf([]byte("old")), Size: 3}}})
	newDir := "independent/" + filepath.Base(local)
	writeAt(t, filepath.Join(root, newDir, "fresh.txt"), "unrelated fresh data")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	t.Logf("ops: %v", sink.ops)
	expected := newDir + "/fresh.txt"
	if _, ok := sink.pushed[expected]; !ok {
		t.Fatalf("new directory was renamed using a deleted FILE basename: expected upload %q, got %v", expected, sink.ops)
	}
}

func TestNestedMoveKeepsClosestSubtreeIdentity(t *testing.T) {
	localname.RestrictForTest(t)
	rows := []row{}
	for _, p := range []string{"A", "A/sub", "B", "B/other"} {
		rows = append(rows, row{n: index.Node{NodeID: p, RelPath: p, LocalPath: p, IsDir: true}})
	}
	rows = append(rows, row{n: index.Node{NodeID: "keep", RelPath: "A/keep.txt", LocalPath: "A/keep.txt", ContentHash: hashOf([]byte("keep"))}})
	rows = append(rows, row{n: index.Node{NodeID: "old", RelPath: "A/sub/report?.md", LocalPath: "A/sub/report？.md", ContentHash: hashOf([]byte("old"))}})
	rows = append(rows, row{n: index.Node{NodeID: "moved", RelPath: "B/other/report？.md", LocalPath: "B/other/report？.md", ContentHash: hashOf([]byte("moved"))}})
	e, root := indexRows(t, rows)
	if err := os.RemoveAll(filepath.Join(root, "A")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "B")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "C/keep.txt"), "keep")
	writeAt(t, filepath.Join(root, "C/sub/report？.md"), "moved")
	paths := map[string]int{}
	for i := 0; i < 100; i++ {
		changes, err := e.DetectLocal()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range changes {
			if c.Op == "create" && !c.IsDir && c.Hash == hashOf([]byte("moved")) {
				paths[c.RelPath]++
			}
		}
	}
	t.Logf("destinations: %v", paths)
	if paths["C/sub/report?.md"] > 0 {
		t.Fatalf("unrelated old subtree overrode closer moved subtree: %v", paths)
	}
}
