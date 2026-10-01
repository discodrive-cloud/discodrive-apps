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
	"discodrive.org/daemon/internal/localname"
)

type failLocalizedUpload struct{ *fakeSink }

func (s failLocalizedUpload) PushFile(ctx context.Context, p string, v *int64, r io.Reader, m time.Time) (RemoteNode, bool, error) {
	if p == "new/report?.md" {
		return RemoteNode{}, false, fmt.Errorf("PUT: 413: upload rejected")
	}
	return s.fakeSink.PushFile(ctx, p, v, r, m)
}

type uploadOnlySink struct{ Sink }

func pendingRenameFixture(t *testing.T, local string) (*Engine, func() *Engine) {
	t.Helper()
	root, db := t.TempDir(), filepath.Join(t.TempDir(), "index.db")
	idx, err := index.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	if err := idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	for _, n := range []index.Node{
		{NodeID: "old", RelPath: "old", IsDir: true},
		{NodeID: "w", RelPath: "old/witness.txt", ContentHash: hashOf([]byte("witness"))},
		{NodeID: "f", RelPath: "old/report?.md", LocalPath: "old/" + local, ContentHash: hashOf([]byte("before"))},
	} {
		if err := idx.Put(n); err != nil {
			t.Fatal(err)
		}
	}
	writeAt(t, filepath.Join(root, "new/witness.txt"), "witness")
	writeAt(t, filepath.Join(root, "new", local), "edited")
	reopen := func() *Engine {
		t.Helper()
		if err := idx.Close(); err != nil {
			t.Fatal(err)
		}
		idx, err = index.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		return New(nil, idx, root)
	}
	return New(nil, idx, root), reopen
}

func TestLocalizedRetryKeepsIdentity(t *testing.T) {
	localname.RestrictForTest(t)
	for _, local := range []string{"report？.md", "report (f).md"} {
		for _, moves := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/moves=%v", local, moves), func(t *testing.T) {
				e, reopen := pendingRenameFixture(t, local)
				failed := failLocalizedUpload{newFakeSink()}
				var sink Sink = failed
				if !moves {
					sink = uploadOnlySink{sink}
				}
				if err := e.PushLocal(context.Background(), sink); err == nil {
					t.Fatal("wanted partial failure")
				}
				if _, ok, err := e.idx.GetByPath("new/witness.txt"); err != nil || !ok {
					t.Fatalf("witness was not consumed: %v", err)
				}
				e = reopen()
				// Further editing must upload current contents, without losing the planned name.
				writeAt(t, filepath.Join(e.root, "new", local), "edited again")
				changes, err := e.DetectLocal()
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, c := range changes {
					if c.Op == "create" && !c.IsDir {
						found = true
						if c.RelPath != "new/report?.md" || c.LocalPath != "new/"+local || c.Hash != hashOf([]byte("edited again")) {
							t.Fatalf("retry lost identity or contents: %+v", c)
						}
					}
				}
				if !found {
					t.Fatal("retry omitted failed file")
				}
				// Even another permanent rejection must retain the old server copy after
				// the original hash witness has disappeared from the scan.
				if err := e.PushLocal(context.Background(), sink); err == nil {
					t.Fatal("wanted repeated rejection")
				}
				for _, deleted := range failed.deleted {
					if deleted == "old" || deleted == "old/report?.md" {
						t.Fatalf("deleted source before upload: %v", failed.deleted)
					}
				}
				good := newFakeSink()
				if err := e.PushLocal(context.Background(), good); err != nil {
					t.Fatal(err)
				}
				if _, ok := good.pushed["new/report?.md"]; !ok {
					t.Fatalf("wrong destination: %v", good.ops)
				}
				if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
					t.Fatalf("not settled: %+v, %v", got, err)
				}
				if err := e.PushLocal(context.Background(), good); err != nil {
					t.Fatal(err)
				}
				if got, err := e.idx.PendingNames(); err != nil || len(got) != 0 {
					t.Fatalf("completed plan retained: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestRemovedPendingFileDoesNotRenameReplacement(t *testing.T) {
	e, reopen := pendingRenameFixture(t, "report？.md")
	if err := e.PushLocal(context.Background(), failLocalizedUpload{newFakeSink()}); err == nil {
		t.Fatal("wanted rejection")
	}
	if err := os.Remove(filepath.Join(e.root, "new/report？.md")); err != nil {
		t.Fatal(err)
	}
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatal(err)
	}
	e = reopen()
	writeAt(t, filepath.Join(e.root, "new/report？.md"), "unrelated replacement")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	if _, ok := sink.pushed["new/report？.md"]; !ok {
		t.Fatalf("stale plan renamed replacement: %v", sink.ops)
	}
}
