package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One change that cannot be applied used to end the whole pull, and since the cursor stayed
// put every later run died on the same entry. On a phone that meant folders appeared and not
// one file did — forever. Everything that can be applied must be, and the failure reported.

func TestPullAppliesWhatItCanAroundAFailure(t *testing.T) {
	good1, good2 := []byte("one"), []byte("two")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(good1), Size: int64(len(good1))},
			{Seq: 2, Op: "create", NodeID: "bad", RelPath: "bad.txt", ContentHash: "deadbeef", Size: 3},
			{Seq: 3, Op: "create", NodeID: "n2", RelPath: "b.txt", ContentHash: hashOf(good2), Size: int64(len(good2))},
		},
		bodies: map[string][][]byte{"n1": {good1}, "n2": {good2}, "bad": {[]byte("xxx")}},
	}
	e, root := newEngine(t, src)

	err := e.PullOnce(context.Background())
	if err == nil {
		t.Fatal("expected the failure to be reported")
	}
	if !strings.Contains(err.Error(), "seq 2") {
		t.Errorf("error should name the change that failed, got: %v", err)
	}
	for _, f := range []struct{ name, want string }{{"a.txt", "one"}, {"b.txt", "two"}} {
		got, rerr := os.ReadFile(filepath.Join(root, f.name))
		if rerr != nil || string(got) != f.want {
			t.Errorf("%s: %q err=%v — a later change must still be applied", f.name, got, rerr)
		}
	}
}

// The cursor may not move past a change that did not apply, or that change is lost: the next
// pull would start after it and never try again.
func TestCursorStopsBeforeTheFailedChange(t *testing.T) {
	good := []byte("one")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(good), Size: int64(len(good))},
			{Seq: 2, Op: "create", NodeID: "bad", RelPath: "bad.txt", ContentHash: "deadbeef", Size: 3},
			{Seq: 3, Op: "create", NodeID: "n2", RelPath: "b.txt", ContentHash: hashOf(good), Size: int64(len(good))},
		},
		bodies: map[string][][]byte{"n1": {good}, "n2": {good}, "bad": {[]byte("xxx")}},
	}
	e, _ := newEngine(t, src)
	_ = e.PullOnce(context.Background())

	cur, err := e.idx.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	if cur != 1 {
		t.Errorf("cursor = %d, want 1 — it must not pass the change that failed", cur)
	}
}

// A clean run still advances to the end.
func TestCursorAdvancesWhenEverythingApplies(t *testing.T) {
	body := []byte("x")
	src := &fakeSource{
		changes: []Change{
			{Seq: 4, Op: "create", NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 7, Op: "create", NodeID: "n2", RelPath: "b.txt", ContentHash: hashOf(body), Size: 1},
		},
		bodies: map[string][][]byte{"n1": {body}, "n2": {body}},
	}
	e, _ := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cur, _ := e.idx.Cursor(); cur != 7 {
		t.Errorf("cursor = %d, want 7", cur)
	}
}
