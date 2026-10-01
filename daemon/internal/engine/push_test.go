package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"discodrive.org/daemon/internal/index"
)

type fakeSink struct {
	pushed  map[string]*int64
	dirs    []string
	deleted []string
	moved   [][2]string // {nodeID, newParentID}
	renamed [][2]string // {nodeID, newName}
	ops     []string    // call order across all methods
	gone    map[string]bool // node ids the server answers "not found" for
}

func newFakeSink() *fakeSink { return &fakeSink{pushed: map[string]*int64{}} }

func (s *fakeSink) PushFile(_ context.Context, rel string, base *int64, r io.Reader, _ time.Time) (RemoteNode, bool, error) {
	b, _ := io.ReadAll(r)
	s.pushed[rel] = base
	s.ops = append(s.ops, "push "+rel)
	return RemoteNode{NodeID: "srv-" + rel, Version: 10, Hash: hashOf(b)}, false, nil
}
func (s *fakeSink) EnsureDir(_ context.Context, rel string) (RemoteNode, error) {
	s.dirs = append(s.dirs, rel)
	s.ops = append(s.ops, "dir "+rel)
	return RemoteNode{NodeID: "dir-" + rel, Version: 1}, nil
}
func (s *fakeSink) DeleteRemote(_ context.Context, rel string) error {
	s.deleted = append(s.deleted, rel)
	s.ops = append(s.ops, "delete "+rel)
	return nil
}
func (s *fakeSink) MoveNode(_ context.Context, nodeID, newParentID string) error {
	if s.gone[nodeID] || s.gone[newParentID] {
		return fmt.Errorf("PATCH move: 404: %w", ErrNodeNotFound)
	}
	s.moved = append(s.moved, [2]string{nodeID, newParentID})
	s.ops = append(s.ops, "move "+nodeID)
	return nil
}
func (s *fakeSink) RenameNode(_ context.Context, nodeID, newName string) error {
	if s.gone[nodeID] {
		return fmt.Errorf("PATCH rename: 404: %w", ErrNodeNotFound)
	}
	s.renamed = append(s.renamed, [2]string{nodeID, newName})
	s.ops = append(s.ops, "rename "+nodeID)
	return nil
}

func TestPushLocalCreate(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644)
	e := New(nil, idx, root)
	sink := newFakeSink()

	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if base, ok := sink.pushed["a.txt"]; !ok || base != nil {
		t.Fatalf("expected push a.txt with base=nil, got ok=%v base=%v", ok, base)
	}
	got, _ := e.DetectLocal()
	if len(got) != 0 {
		t.Fatalf("after push, DetectLocal must be empty (echo), got %+v", got)
	}
}

func TestPushLocalUpdateSendsBaseVersion(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("v2"), 0o644)
	idx.Put(index.Node{NodeID: "n1", RelPath: "a.txt", Version: 5, ContentHash: hashOf([]byte("v1")), Size: 2})
	e := New(nil, idx, root)
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	base := sink.pushed["a.txt"]
	if base == nil || *base != 5 {
		t.Fatalf("expected base=5, got %v", base)
	}
}

func TestPushLocalRenameInPlaceUsesRename(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	body := []byte("same content")
	os.WriteFile(filepath.Join(root, "b.txt"), body, 0o644)
	idx.Put(index.Node{NodeID: "n1", RelPath: "a.txt", Version: 5, ContentHash: hashOf(body), Size: int64(len(body))})
	e := New(nil, idx, root)
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.renamed) != 1 || sink.renamed[0] != [2]string{"n1", "b.txt"} {
		t.Fatalf("expected rename n1 -> b.txt, got %+v", sink.renamed)
	}
	if len(sink.moved) != 0 {
		t.Fatalf("parent unchanged — no move expected, got %+v", sink.moved)
	}
	if len(sink.pushed) != 0 || len(sink.deleted) != 0 {
		t.Fatalf("rename must not re-upload or delete: pushed=%+v deleted=%+v", sink.pushed, sink.deleted)
	}
	if n, ok, _ := idx.Get("n1"); !ok || n.RelPath != "b.txt" {
		t.Fatalf("index must track the new path, got %+v ok=%v", n, ok)
	}
	if got, _ := e.DetectLocal(); len(got) != 0 {
		t.Fatalf("after push, DetectLocal must be empty, got %+v", got)
	}
}

func TestPushLocalMoveAcrossDirsUsesMove(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	body := []byte("payload")
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "dst"), 0o755)
	os.WriteFile(filepath.Join(root, "dst", "a.txt"), body, 0o644)
	idx.Put(index.Node{NodeID: "d-src", RelPath: "src", IsDir: true, Version: 1})
	idx.Put(index.Node{NodeID: "d-dst", RelPath: "dst", IsDir: true, Version: 1})
	idx.Put(index.Node{NodeID: "n1", RelPath: "src/a.txt", Version: 5, ContentHash: hashOf(body), Size: int64(len(body))})
	e := New(nil, idx, root)
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.moved) != 1 || sink.moved[0] != [2]string{"n1", "d-dst"} {
		t.Fatalf("expected move n1 -> d-dst, got %+v", sink.moved)
	}
	if len(sink.renamed) != 0 {
		t.Fatalf("same basename — no rename expected, got %+v", sink.renamed)
	}
	if len(sink.pushed) != 0 || len(sink.deleted) != 0 {
		t.Fatalf("move must not re-upload or delete: pushed=%+v deleted=%+v", sink.pushed, sink.deleted)
	}
}

func TestPushLocalDirRenameMovesFilesBeforeDeletingOldDir(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	body := []byte("note")
	// Local dir rename old -> new: on disk only the new tree exists.
	os.MkdirAll(filepath.Join(root, "new"), 0o755)
	os.WriteFile(filepath.Join(root, "new", "a.txt"), body, 0o644)
	idx.Put(index.Node{NodeID: "d-old", RelPath: "old", IsDir: true, Version: 1})
	idx.Put(index.Node{NodeID: "n1", RelPath: "old/a.txt", Version: 5, ContentHash: hashOf(body), Size: int64(len(body))})
	e := New(nil, idx, root)
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.moved) != 1 || sink.moved[0] != [2]string{"n1", "dir-new"} {
		t.Fatalf("expected move n1 -> dir-new, got %+v", sink.moved)
	}
	if len(sink.pushed) != 0 {
		t.Fatalf("file content must not be re-uploaded, got %+v", sink.pushed)
	}
	if len(sink.deleted) != 1 || sink.deleted[0] != "old" {
		t.Fatalf("only the old dir must be deleted, got %+v", sink.deleted)
	}
	// The old dir is deleted recursively on the server, so the file must be moved
	// out of it BEFORE the delete.
	var moveAt, delAt int
	for i, op := range sink.ops {
		switch op {
		case "move n1":
			moveAt = i
		case "delete old":
			delAt = i
		}
	}
	if moveAt > delAt {
		t.Fatalf("move must precede delete of the old dir, ops=%v", sink.ops)
	}
	if got, _ := e.DetectLocal(); len(got) != 0 {
		t.Fatalf("after push, DetectLocal must be empty, got %+v", got)
	}
}

func TestPushLocalDelete(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true) // pushes only happen from an established mirror
	idx.Put(index.Node{NodeID: "n1", RelPath: "gone.txt", Version: 3, ContentHash: hashOf([]byte("x")), Size: 1})
	e := New(nil, idx, root)
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.deleted) != 1 || sink.deleted[0] != "gone.txt" {
		t.Fatalf("expected delete gone.txt, got %+v", sink.deleted)
	}
	if _, ok, _ := idx.Get("n1"); ok {
		t.Fatalf("node must be removed from the index")
	}
}

// A local rename of a file whose server node is gone (hard-deleted, its delete event lost):
// the rename cannot be applied, so the stale index entry is dropped and the file goes up
// as a new one — the local copy is never lost, and the pass does not fail on it forever.
func TestPushLocalRenameOfGoneNodeUploadsAsNew(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true)
	body := []byte("same content")
	os.WriteFile(filepath.Join(root, "b.txt"), body, 0o644)
	os.WriteFile(filepath.Join(root, "other.txt"), []byte("other"), 0o644)
	idx.Put(index.Node{NodeID: "ghost", RelPath: "a.txt", Version: 5, ContentHash: hashOf(body), Size: int64(len(body))})
	e := New(nil, idx, root)
	sink := newFakeSink()
	sink.gone = map[string]bool{"ghost": true}

	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v, want the pass to succeed", err)
	}
	if _, ok, _ := idx.Get("ghost"); ok {
		t.Fatal("the gone node must leave the index")
	}
	if base, ok := sink.pushed["b.txt"]; !ok || base != nil {
		t.Fatalf("b.txt must go up as a new file (base nil), got ok=%v base=%v", ok, base)
	}
	if _, ok := sink.pushed["other.txt"]; !ok {
		t.Fatal("the rest of the pass must still run")
	}
	if len(sink.deleted) != 0 {
		t.Fatalf("nothing may be deleted, got %v", sink.deleted)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("local file must stay: %v", err)
	}
	if got, _ := e.DetectLocal(); len(got) != 0 {
		t.Fatalf("after the pass nothing is left to push, got %+v", got)
	}
}

// A local move into a folder whose server node is gone: the server answers the same "not
// found" as for a gone file, and the engine does not ask which. Current behaviour, kept on
// purpose: the moved file's index entry is dropped and the file goes up as new at its new
// path (the server recreates the folder chain). The old server copy may stay where it was —
// a duplicate at worst — but nothing is deleted anywhere and the local file stays.
func TestPushLocalMoveIntoGoneFolderUploadsAsNew(t *testing.T) {
	root := t.TempDir()
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true)
	body := []byte("payload")
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "dst"), 0o755)
	os.WriteFile(filepath.Join(root, "dst", "a.txt"), body, 0o644)
	idx.Put(index.Node{NodeID: "d-src", RelPath: "src", IsDir: true, Version: 1})
	idx.Put(index.Node{NodeID: "d-dst", RelPath: "dst", IsDir: true, Version: 1})
	idx.Put(index.Node{NodeID: "n1", RelPath: "src/a.txt", Version: 5, ContentHash: hashOf(body), Size: int64(len(body))})
	e := New(nil, idx, root)
	sink := newFakeSink()
	sink.gone = map[string]bool{"d-dst": true}

	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v, want the pass to succeed", err)
	}
	if base, ok := sink.pushed["dst/a.txt"]; !ok || base != nil {
		t.Fatalf("dst/a.txt must go up as a new file (base nil), got ok=%v base=%v", ok, base)
	}
	if _, ok, _ := idx.Get("n1"); ok {
		t.Fatal("the moved node's stale entry must leave the index")
	}
	if len(sink.deleted) != 0 {
		t.Fatalf("nothing may be deleted, got %v", sink.deleted)
	}
	if got, err := os.ReadFile(filepath.Join(root, "dst", "a.txt")); err != nil || string(got) != "payload" {
		t.Fatalf("local file must stay: %q %v", got, err)
	}
}
