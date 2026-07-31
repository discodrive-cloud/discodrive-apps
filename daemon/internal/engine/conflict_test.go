package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"discodrive.org/daemon/internal/index"
)

// conflictSink: PushFile always returns conflicted=true.
type conflictSink struct{ pushed []string }

func (s *conflictSink) PushFile(_ context.Context, rel string, _ *int64, r io.Reader, _ time.Time) (RemoteNode, bool, error) {
	io.Copy(io.Discard, r)
	s.pushed = append(s.pushed, rel)
	return RemoteNode{NodeID: "conflict-copy", Version: 0}, true, nil
}
func (s *conflictSink) EnsureDir(_ context.Context, rel string) (RemoteNode, error) {
	return RemoteNode{NodeID: "dir-" + rel, Version: 1}, nil
}
func (s *conflictSink) DeleteRemote(_ context.Context, rel string) error { return nil }

func TestConflictTwoSidedEdit(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()

	// local edit present; the index knows a.txt (version 5, old base-hash)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("local-edit"), 0o644)
	idx.Put(index.Node{NodeID: "n1", RelPath: "a.txt", Version: 5, ContentHash: hashOf([]byte("base")), Size: 4})

	// PUSH → conflicted; index must NOT change
	e := New(nil, idx, root)
	sink := &conflictSink{}
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := idx.Get("n1")
	if !ok || got.Version != 5 || got.ContentHash != hashOf([]byte("base")) {
		t.Fatalf("after a conflicted push, the a.txt index must not change, got %+v ok=%v", got, ok)
	}

	// PULL: server delivers its main version of a.txt (server-edit) + the conflict copy (our local-edit)
	serverEdit := []byte("server-edit")
	ourCopy := []byte("local-edit")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(serverEdit), Version: 6, Size: int64(len(serverEdit))},
			{Seq: 2, NodeID: "n2", RelPath: "a (conflict, dev, 2026-06-12).txt", ContentHash: hashOf(ourCopy), Version: 1, Size: int64(len(ourCopy))},
		},
		bodies: map[string][][]byte{"n1": {serverEdit}, "n2": {ourCopy}},
	}
	e2 := New(src, idx, root)
	if err := e2.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// server version won in a.txt; ours is preserved as a conflict copy — both intact
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "server-edit" {
		t.Fatalf("a.txt must become the server version: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a (conflict, dev, 2026-06-12).txt")); string(b) != "local-edit" {
		t.Fatalf("the conflict copy must keep our edit: %q", b)
	}

	// no infinite loop: after a full push→pull cycle, DetectLocal must return nothing
	left, _ := e2.DetectLocal()
	if len(left) != 0 {
		t.Fatalf("no local changes must remain after the cycle, got %+v", left)
	}
}
