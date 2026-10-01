package engine

import (
	"context"
	"discodrive.org/daemon/internal/index"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSecondRenamePendingIdentity(t *testing.T) {
	e, _ := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected failure")
	}
	if err := os.Rename(filepath.Join(e.root, "new"), filepath.Join(e.root, "newest")); err != nil {
		t.Fatal(err)
	}
	cs, err := e.DetectLocal()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.Op == "create" && !c.IsDir {
			if c.RelPath == "newest/report？.md" {
				t.Fatalf("pending identity lost after repeated folder rename: %+v", c)
			}
		}
	}
}

type rejectAllUploads struct{ *fakeSink }

func (s rejectAllUploads) PushFile(ctx context.Context, p string, v *int64, r io.Reader, m time.Time) (RemoteNode, bool, error) {
	return RemoteNode{}, false, fmt.Errorf("PUT: 413: upload rejected")
}
func TestSecondRenameRetainsSource(t *testing.T) {
	e, _ := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected initial failure")
	}
	if err := os.Rename(filepath.Join(e.root, "new"), filepath.Join(e.root, "newest")); err != nil {
		t.Fatal(err)
	}
	s := rejectAllUploads{newFakeSink()}
	if err := e.PushLocal(context.Background(), s); err == nil {
		t.Fatal("expected failure")
	}
	for _, p := range s.deleted {
		if p == "old" || p == "old/report?.md" {
			t.Fatalf("deleted only server source after failed retry: %v", s.deleted)
		}
	}
}

func TestCompletedPlanDoesNotRenameNewFile(t *testing.T) {
	e, _ := pendingRenameFixture(t, "report？.md")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	n, ok, err := e.idx.GetByPath("new/report?.md")
	if err != nil || !ok {
		t.Fatal("no uploaded file", err)
	}
	e.src = &fakeSource{changes: []Change{{Seq: 99, Op: "delete", Deleted: true, NodeID: n.NodeID, RelPath: n.RelPath}}}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(e.root, "new/report？.md")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("remote deletion not applied", err)
	}
	writeAt(t, p, "new unrelated file")
	good := newFakeSink()
	if err := e.PushLocal(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if _, ok := good.pushed["new/report？.md"]; !ok {
		t.Fatalf("completed plan renamed a new file: %v", good.ops)
	}
}

func TestRepeatedRenameKeepsOriginalSourceAcrossRestart(t *testing.T) {
	e, reopen := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected failure")
	}
	old := "new"
	for _, next := range []string{"second", "third", "fourth"} {
		e = reopen()
		if err := os.Rename(filepath.Join(e.root, old), filepath.Join(e.root, next)); err != nil {
			t.Fatal(err)
		}
		writeAt(t, filepath.Join(e.root, next, "report？.md"), "edited in "+next)
		sink := rejectAllUploads{newFakeSink()}
		if err := e.PushLocal(context.Background(), sink); err == nil {
			t.Fatal("expected failure")
		}
		for _, p := range sink.deleted {
			if p == "old" || p == "old/report?.md" {
				t.Fatalf("deleted source during %s: %v", next, sink.deleted)
			}
		}
		names, err := e.idx.PendingNames()
		if err != nil {
			t.Fatal(err)
		}
		if len(names) != 1 || names[0].LocalPath != next+"/report？.md" || names[0].RelPath != next+"/report?.md" || names[0].SourcePath != "old/report?.md" {
			t.Fatalf("wrong pending plan after %s: %+v", next, names)
		}
		old = next
	}
	e = reopen()
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.pushed["fourth/report?.md"]; !ok {
		t.Fatalf("wrong final path: %v", sink.ops)
	}
	if names, err := e.idx.PendingNames(); err != nil || len(names) != 0 {
		t.Fatalf("completed plan remains: %+v %v", names, err)
	}
}

func TestPendingFileRenameRetainsSource(t *testing.T) {
	e, _ := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected initial failure")
	}
	if err := os.Rename(filepath.Join(e.root, "new/report？.md"), filepath.Join(e.root, "new/final.md")); err != nil {
		t.Fatal(err)
	}
	s := rejectAllUploads{newFakeSink()}
	if err := e.PushLocal(context.Background(), s); err == nil {
		t.Fatal("expected failure")
	}
	for _, p := range s.deleted {
		if p == "old" || p == "old/report?.md" {
			t.Fatalf("deleted only server source after failed renamed-file retry: %v", s.deleted)
		}
	}
}

func TestEditedPendingRenameRetainsSourceUntilUploaded(t *testing.T) {
	e, reopen := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected failure")
	}
	for _, next := range []string{"final.md", "last.md"} {
		old := "report？.md"
		if next == "last.md" {
			old = "final.md"
		}
		if err := os.Rename(filepath.Join(e.root, "new", old), filepath.Join(e.root, "new", next)); err != nil {
			t.Fatal(err)
		}
		writeAt(t, filepath.Join(e.root, "new", next), "changed during rename to "+next)
		sink := rejectAllUploads{newFakeSink()}
		for attempt := 0; attempt < 2; attempt++ {
			e = reopen()
			if err := e.PushLocal(context.Background(), sink); err == nil {
				t.Fatal("expected rejection")
			}
			for _, p := range sink.deleted {
				if p == "old" || p == "old/report?.md" {
					t.Fatalf("deleted possible source: %v", sink.deleted)
				}
			}
		}
	}
	good := newFakeSink()
	if err := e.PushLocal(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	if _, ok := good.pushed["new/last.md"]; !ok {
		t.Fatalf("explicit rename lost: %v", good.ops)
	}
	deletedSource := false
	for _, p := range good.deleted {
		if p == "old/report?.md" {
			deletedSource = true
		}
	}
	if !deletedSource {
		t.Fatalf("source held after successful upload: %v", good.ops)
	}
	if names, err := e.idx.PendingNames(); err != nil || len(names) != 0 {
		t.Fatalf("stale holds: %+v %v", names, err)
	}
}

type rejectPairedMove struct{ *fakeSink }

func (s rejectPairedMove) MoveNode(context.Context, string, string) error {
	return fmt.Errorf("PATCH: 413: rejected")
}
func (s rejectPairedMove) RenameNode(context.Context, string, string) error {
	return fmt.Errorf("PATCH: 413: rejected")
}
func TestPairingPreservesPendingSource(t *testing.T) {
	e, _ := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("expected initial rejection")
	}
	if err := e.idx.Put(index.Node{NodeID: "unrelated", RelPath: "elsewhere.md", ContentHash: hashOf([]byte("edited"))}); err != nil {
		t.Fatal(err)
	}
	s := rejectPairedMove{newFakeSink()}
	if err := e.PushLocal(context.Background(), s); err == nil {
		t.Fatal("expected failure")
	}
	for _, p := range s.deleted {
		if p == "old" || p == "old/report?.md" {
			t.Fatalf("deleted pending source while paired move failed: %v", s.deleted)
		}
	}
}
