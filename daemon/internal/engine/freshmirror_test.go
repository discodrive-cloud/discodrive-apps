package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// After pairing the server is the truth: a folder that existed before the first pull is
// not "new files to upload" but the previous life of this device, and it is set aside
// untouched while a clean folder is filled from the server.

func mustWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func serverWithOneFile() (*fakeSource, []byte) {
	body := []byte("from the server")
	return &fakeSource{
		changes: []Change{{Seq: 1, Op: "create", NodeID: "n1", RelPath: "new.txt", ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body, body, body}}, // one per pull that re-downloads it
	}, body
}

func TestNeverPulledIndexPushesNothing(t *testing.T) {
	e, root := newEngine(t, &fakeSource{})
	mustWrite(t, root, "old.txt", "stale")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if len(sink.ops) != 0 {
		t.Fatalf("pushed %v before the first pull; the server is the truth after pairing", sink.ops)
	}
}

func TestFirstPullSetsTheOldFolderAside(t *testing.T) {
	src, body := serverWithOneFile()
	e, root := newEngine(t, src)
	mustWrite(t, root, "old.txt", "stale")
	mustWrite(t, root, "sub/x.txt", "stale too")

	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if got := names(t, root); len(got) != 1 || got[0] != "new.txt" {
		t.Fatalf("root after the first pull = %v, want only the server's new.txt", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(b) != string(body) {
		t.Fatalf("new.txt = %q", b)
	}
	aside := e.SetAside()
	if aside == "" {
		t.Fatal("SetAside() is empty, the old folder was not reported")
	}
	if filepath.Dir(aside) != filepath.Dir(root) || !strings.HasPrefix(filepath.Base(aside), filepath.Base(root)+".old-") {
		t.Fatalf("set-aside folder %q is not a sibling named after the root %q", aside, root)
	}
	if b, err := os.ReadFile(filepath.Join(aside, "old.txt")); err != nil || string(b) != "stale" {
		t.Fatalf("old.txt in the set-aside folder: %q err=%v", b, err)
	}
	if _, err := os.Stat(filepath.Join(aside, "sub", "x.txt")); err != nil {
		t.Fatalf("sub/x.txt in the set-aside folder: %v", err)
	}
	// A pass after that is an ordinary one: the mirror is established.
	sink := newFakeSink()
	mustWrite(t, root, "mine.txt", "written after pairing")
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if _, ok := sink.pushed["mine.txt"]; !ok {
		t.Fatalf("a file created after the first pull was not pushed: %v", sink.ops)
	}
}

func TestEmptyFolderIsNotSetAside(t *testing.T) {
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	mustWrite(t, root, ".DS_Store", "junk")
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if e.SetAside() != "" {
		t.Fatalf("a folder holding only OS junk was set aside to %q", e.SetAside())
	}
}

func TestEmptyServerStillEstablishesTheMirror(t *testing.T) {
	// A brand-new account has no changes at all; the pull must still mark the folder as
	// the mirror, or nothing could ever be uploaded from it.
	e, root := newEngine(t, &fakeSource{})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustWrite(t, root, "first.txt", "hello")
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if _, ok := sink.pushed["first.txt"]; !ok {
		t.Fatalf("nothing pushed after an empty first pull: %v", sink.ops)
	}
}

func TestSyncedFolderIsNeverSetAside(t *testing.T) {
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "mine.txt", "local work")
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetAside() != "" {
		t.Fatalf("an established mirror was set aside to %q", e.SetAside())
	}
	if _, err := os.Stat(filepath.Join(root, "mine.txt")); err != nil {
		t.Fatalf("mine.txt vanished from the mirror: %v", err)
	}
}

func TestResetKeepingFilesRebuildsTheIndexInPlace(t *testing.T) {
	// The "download everything again" recovery: the folder IS the mirror and stays.
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "mine.txt", "local work")
	if err := e.ResetIndexKeepingFiles(); err != nil {
		t.Fatal(err)
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetAside() != "" {
		t.Fatalf("a reset that keeps files set the folder aside to %q", e.SetAside())
	}
	if got := names(t, root); len(got) != 2 {
		t.Fatalf("root = %v, want new.txt and mine.txt", got)
	}
	// The allowance is for one reset only: a later wipe is a fresh pairing again.
	if err := e.idx.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetAside() == "" {
		t.Fatal("a wiped index with a full folder was not set aside")
	}
}

func TestInitialScopePreservesPreviousContents(t *testing.T) {
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	mustWrite(t, root, "previous.md", "keep me")
	if err := e.ResetForScope(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(e.SetAside(), "previous.md"))
	if err != nil || string(b) != "keep me" {
		t.Fatalf("previous contents not preserved: %q, %v", b, err)
	}
	if epoch, err := e.ScopeEpoch(); epoch != 7 || err != nil {
		t.Fatalf("epoch: %d, %v", epoch, err)
	}
}

func TestPreparedMirrorNeverReplacesRootDuringFirstPull(t *testing.T) {
	src, _ := serverWithOneFile()
	base, root := newEngine(t, src)
	e := NewPrepared(src, base.idx, root)
	before, _ := os.Stat(root)
	mustWrite(t, root, "local.md", "keep me")
	// Repeated preparation before MirrorReady simulates a failed first download.
	for n := 0; n < 2; n++ {
		if err := e.establishMirror(); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.ResetForScope(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(root)
	if !os.SameFile(before, after) {
		t.Fatal("security-scoped root replaced")
	}
	if b, err := os.ReadFile(filepath.Join(root, "local.md")); err != nil || string(b) != "keep me" {
		t.Fatal("local file swept during first scope", err)
	}
}

func TestInterruptedScopeChangeStillSweepsOnRetry(t *testing.T) {
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "old-scope.md", "must not enter new scope")
	// Missing content forces the first rebuild to stop after resetting the index.
	bodies := src.bodies
	src.bodies = map[string][][]byte{}
	if err := e.ResetForScope(context.Background(), 7); err == nil {
		t.Fatal("expected failed download")
	}
	src.bodies = bodies
	if err := e.ResetForScope(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old-scope.md")); !os.IsNotExist(err) {
		t.Fatal("old-scope file survived retry", err)
	}
}
