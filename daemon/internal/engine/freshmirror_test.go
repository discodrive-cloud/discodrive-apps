package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/index"
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
	// A file synced under the old scope: its content is on the server, so the sweep
	// removes it. An unsynced one next to it is the user's only copy and stays.
	mustWrite(t, root, "old-scope.md", "must not enter new scope")
	if err := e.idx.Put(index.Node{NodeID: "old1", RelPath: "old-scope.md", Version: 1,
		ContentHash: hashOf([]byte("must not enter new scope")), Size: 24}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "offline.md", "written offline")
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
	if b, err := os.ReadFile(filepath.Join(root, "offline.md")); err != nil || string(b) != "written offline" {
		t.Fatalf("unsynced file swept: %q, %v", b, err)
	}
}

// The snapshot a failed reset keeps is for its retry only. Once a normal pull has succeeded
// (the scope went back to the one the folder was in), the folder is reconciled and a later
// reset must look at the index as it is then, not at a snapshot of what it once was.
func TestFailedScopeResetSnapshotDoesNotOutliveANormalPull(t *testing.T) {
	src, _ := serverWithOneFile()
	e, root := newEngine(t, src)
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "kept.md", "user work")
	if err := e.idx.Put(index.Node{NodeID: "k1", RelPath: "kept.md", Version: 1,
		ContentHash: hashOf([]byte("user work")), Size: 9}); err != nil {
		t.Fatal(err)
	}
	bodies := src.bodies
	src.bodies = map[string][][]byte{}
	if err := e.ResetForScope(context.Background(), 7); err == nil {
		t.Fatal("expected failed download")
	}
	// The scope went back: the syncer runs an ordinary pass, which succeeds. kept.md is
	// now a local file the index does not know (the next push would upload it).
	src.bodies = bodies
	// The failed reset re-armed the keep-local allowance its pull used up, so this pull
	// keeps the folder as the mirror instead of setting it aside.
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.SetAside() != "" {
		t.Fatalf("a failed reset followed by the old scope set the folder aside to %q", e.SetAside())
	}
	if before, err := e.idx.BeforeReset(); err != nil || len(before) != 0 {
		t.Fatalf("the stored snapshot outlived a normal pull: %v, %v", before, err)
	}
	if err := e.ResetForScope(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "kept.md")); err != nil || string(b) != "user work" {
		t.Fatalf("a stale snapshot swept kept.md: %q, %v", b, err)
	}
}

// The pre-reset snapshot is on disk: a reset interrupted by a restart (the laptop sleeps,
// the phone kills the app) still sweeps the old scope's synced files on the retry, keeps
// the user's own, and leaves nothing of the old scope for the next push to upload.
func TestScopeResetSurvivesARestart(t *testing.T) {
	src, _ := serverWithOneFile()
	root := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	idx, err := index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	e := New(src, idx, root)
	if err := idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	synced := map[string]string{"old/a.md": "old scope a", "old/b.md": "old scope b", "edited.md": "as synced"}
	n := 0
	for rel, body := range synced {
		n++
		mustWrite(t, root, rel, body)
		if err := idx.Put(index.Node{NodeID: "old" + string(rune('0'+n)), RelPath: rel, Version: 1,
			ContentHash: hashOf([]byte(body)), Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.Put(index.Node{NodeID: "olddir", RelPath: "old", IsDir: true, Version: 1}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "edited.md", "edited since")
	mustWrite(t, root, "offline.md", "written offline")

	bodies := src.bodies
	src.bodies = map[string][][]byte{}
	if err := e.ResetForScope(context.Background(), 7); err == nil {
		t.Fatal("expected failed download")
	}
	// Restart: a new engine on the reopened index, nothing kept in memory.
	idx.Close()
	idx, err = index.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	src.bodies = bodies
	e = New(src, idx, root)
	if err := e.ResetForScope(context.Background(), 7); err != nil {
		t.Fatalf("retry after restart: %v", err)
	}

	for _, rel := range []string{"old/a.md", "old/b.md", "old"} {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("old-scope %s survived the retry after a restart: %v", rel, err)
		}
	}
	for rel, want := range map[string]string{"edited.md": "edited since", "offline.md": "written offline", "new.txt": "from the server"} {
		if b, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(b) != want {
			t.Fatalf("%s = %q, %v; want %q", rel, b, err, want)
		}
	}
	changes, err := e.DetectLocal()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if strings.HasPrefix(c.RelPath, "old") {
			t.Fatalf("old-scope file reported for upload after the reset: %+v", c)
		}
	}
	if before, err := idx.BeforeReset(); err != nil || len(before) != 0 {
		t.Fatalf("snapshot left behind after the reset completed: %v, %v", before, err)
	}
	if epoch, _ := e.ScopeEpoch(); epoch != 7 {
		t.Fatalf("epoch = %d, want 7", epoch)
	}
}
