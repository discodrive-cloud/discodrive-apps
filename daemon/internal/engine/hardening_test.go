package engine

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
)

// --- H1: a scope reset keeps files the server never knew or that were edited since ---

func scopeFixture(t *testing.T) (*Engine, *fakeSource, string, []byte) {
	t.Helper()
	body := []byte("server a")
	src := &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "dir/a.txt", ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	writeAt(t, filepath.Join(root, "dir", "new.md"), "offline note")
	writeAt(t, filepath.Join(root, "other", "x.md"), "another offline note")
	return e, src, root, body
}

func writeAt(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, p, want string) {
	t.Helper()
	got, err := os.ReadFile(p)
	if err != nil || string(got) != want {
		t.Fatalf("%s: %q err=%v, want %q", p, got, err, want)
	}
}

func mustBeGone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("%s should be gone, err=%v", p, err)
	}
}

func TestResetForScopeKeepsUnsyncedFilesWhenScopeStillHasThem(t *testing.T) {
	e, src, root, body := scopeFixture(t)
	src.changes = []Change{{Seq: 10, NodeID: "n1", RelPath: "dir/a.txt", ContentHash: hashOf(body), Size: int64(len(body))}}
	if err := e.ResetForScope(context.Background(), 2); err != nil {
		t.Fatalf("ResetForScope: %v", err)
	}
	mustRead(t, filepath.Join(root, "dir", "a.txt"), "server a")
	mustRead(t, filepath.Join(root, "dir", "new.md"), "offline note")
	mustRead(t, filepath.Join(root, "other", "x.md"), "another offline note")
}

func TestResetForScopeRemovesOnlyUnchangedServerFiles(t *testing.T) {
	e, src, root, _ := scopeFixture(t)
	src.changes = nil // the new scope no longer includes dir/a.txt
	if err := e.ResetForScope(context.Background(), 2); err != nil {
		t.Fatalf("ResetForScope: %v", err)
	}
	mustBeGone(t, filepath.Join(root, "dir", "a.txt"))
	mustRead(t, filepath.Join(root, "dir", "new.md"), "offline note")
	mustRead(t, filepath.Join(root, "other", "x.md"), "another offline note")
}

func TestResetForScopeKeepsEditedServerFile(t *testing.T) {
	e, src, root, _ := scopeFixture(t)
	writeAt(t, filepath.Join(root, "dir", "a.txt"), "edited offline")
	src.changes = nil
	if err := e.ResetForScope(context.Background(), 2); err != nil {
		t.Fatalf("ResetForScope: %v", err)
	}
	mustRead(t, filepath.Join(root, "dir", "a.txt"), "edited offline")
}

func TestResetForScopeRemovesFoldersLeftEmpty(t *testing.T) {
	body := []byte("gone")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "old", IsDir: true},
			{Seq: 2, NodeID: "n1", RelPath: "old/sub/a.txt", ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	src.changes = nil
	if err := e.ResetForScope(context.Background(), 2); err != nil {
		t.Fatalf("ResetForScope: %v", err)
	}
	mustBeGone(t, filepath.Join(root, "old"))
}

// --- M1: a server rename never overwrites an unsynced file at the destination ---

func TestServerRenameKeepsUnsyncedFileAtDestination(t *testing.T) {
	body := []byte("server content")
	src := &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "a.txt", Version: 1, ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	writeAt(t, filepath.Join(root, "b.txt"), "local only")
	src.changes = append(src.changes, Change{Seq: 2, NodeID: "n1", RelPath: "b.txt", Version: 2, ContentHash: hashOf(body), Size: int64(len(body))})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, "b.txt"), "server content")
	mustBeGone(t, filepath.Join(root, "a.txt"))
	copies, _ := filepath.Glob(filepath.Join(root, "b"+localConflictTag+"*).txt"))
	if len(copies) != 1 {
		t.Fatalf("want one local conflict copy, got %v", copies)
	}
	mustRead(t, copies[0], "local only")
}

func TestServerDirRenameDoesNotReplaceExistingFolder(t *testing.T) {
	body := []byte("inside")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "a", IsDir: true},
			{Seq: 2, NodeID: "n1", RelPath: "a/f.txt", ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	// A folder the user made, holding unsynced work, where the server now moves "a":
	// the rename must not go over it; the server's files are placed into it instead.
	if err := os.MkdirAll(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "b", "mine.txt"), "mine")
	src.changes = append(src.changes,
		Change{Seq: 3, NodeID: "d1", RelPath: "b", IsDir: true},
		Change{Seq: 4, NodeID: "n1", RelPath: "b/f.txt", ContentHash: hashOf(body), Size: int64(len(body))},
	)
	src.bodies["n1"] = [][]byte{body}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, "b", "mine.txt"), "mine")
	mustRead(t, filepath.Join(root, "b", "f.txt"), "inside")
}

// --- M2: an empty rel_path for a file never writes outside the sync root ---

func TestApplyRefusesToWriteSyncRoot(t *testing.T) {
	body := []byte("payload")
	calls := 0
	src := &countingSource{
		fakeSource: fakeSource{
			changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "", ContentHash: hashOf(body), Size: int64(len(body))}},
			bodies:  map[string][][]byte{"n1": {body}},
		},
		calls: &calls,
	}
	e, root := newEngine(t, src)
	err := e.PullOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sync root") {
		t.Fatalf("PullOnce error = %v, want a refusal naming the sync root", err)
	}
	if calls != 0 {
		t.Fatalf("content was downloaded %d time(s) for a path that is the sync root", calls)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(root), ".kf-tmp-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files next to the root: %v", leftovers)
	}
}

// --- L1: a delete for a node the index never had touches nothing ---

func TestDeleteOfUnknownNodeLeavesFileAlone(t *testing.T) {
	body := []byte("synced")
	src := &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	src.changes = append(src.changes, Change{Seq: 2, NodeID: "ghost", RelPath: "a.txt", Deleted: true})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, "a.txt"), "synced")
	if _, ok, _ := e.idx.Get("n1"); !ok {
		t.Fatal("n1 dropped from the index by another node's delete")
	}
}

// --- M3: one rejected upload does not stop the rest of the push ---

type rejectingSink struct {
	fakeSink
	reject string
	err    error
}

func (s *rejectingSink) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, mt time.Time) (RemoteNode, bool, error) {
	if rel == s.reject {
		io.Copy(io.Discard, r)
		s.ops = append(s.ops, "reject "+rel)
		return RemoteNode{}, false, s.err
	}
	return s.fakeSink.PushFile(ctx, rel, base, r, mt)
}

func pushFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	idx.SetMirrorReady(true)
	writeAt(t, filepath.Join(root, "a-big.bin"), "too big")
	writeAt(t, filepath.Join(root, "b.txt"), "fine")
	return New(nil, idx, root), root
}

func TestPushLocalContinuesPastRejectedFile(t *testing.T) {
	e, _ := pushFixture(t)
	sink := &rejectingSink{fakeSink: *newFakeSink(), reject: "a-big.bin",
		err: errors.New("PUT /sync/file: 413 Request Entity Too Large")}
	err := e.PushLocal(context.Background(), sink)
	if err == nil || !strings.Contains(err.Error(), "a-big.bin") {
		t.Fatalf("PushLocal error = %v, want one naming a-big.bin", err)
	}
	var pf *PushFailures
	if !errors.As(err, &pf) {
		t.Fatalf("PushLocal error %T is not a *PushFailures", err)
	}
	if _, ok := sink.pushed["b.txt"]; !ok {
		t.Fatalf("b.txt was not pushed after a-big.bin was rejected; ops=%v", sink.ops)
	}
	if _, ok, _ := e.idx.GetByPath("a-big.bin"); ok {
		t.Fatal("rejected file must stay out of the index so it is retried")
	}
}

func TestPushLocalStopsOnTransportOrAuthError(t *testing.T) {
	for name, perr := range map[string]error{
		"network": &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
		"auth":    errors.New("PUT /sync/file: 401 Unauthorized"),
		"token":   errors.New("device token exchange: 403"),
		"chunked": errors.New("/upload/init: 401: token expired"),
	} {
		t.Run(name, func(t *testing.T) {
			e, _ := pushFixture(t)
			sink := &rejectingSink{fakeSink: *newFakeSink(), reject: "a-big.bin", err: perr}
			err := e.PushLocal(context.Background(), sink)
			if err == nil {
				t.Fatal("PushLocal returned nil")
			}
			var pf *PushFailures
			if errors.As(err, &pf) {
				t.Fatalf("a %s error must abort the pass, got per-file failures: %v", name, err)
			}
			if _, ok := sink.pushed["b.txt"]; ok {
				t.Fatalf("push went on after a %s error", name)
			}
		})
	}
}

// --- M4: two server names that are one name on disk get distinct local names ---

func caseInsensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	p := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

func TestCaseVariantServerNamesDoNotCollideOnDisk(t *testing.T) {
	upper, lower := []byte("upper"), []byte("lower")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "Foo.md", Version: 1, ContentHash: hashOf(upper), Size: int64(len(upper))},
			{Seq: 2, NodeID: "n2", RelPath: "foo.md", Version: 1, ContentHash: hashOf(lower), Size: int64(len(lower))},
		},
		bodies: map[string][][]byte{"n1": {upper}, "n2": {lower}},
	}
	e, root := newEngine(t, src)
	if !caseInsensitiveFS(t, root) {
		t.Skip("filesystem is case-sensitive; the names cannot collide here")
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	n1, _, _ := e.idx.Get("n1")
	n2, _, _ := e.idx.Get("n2")
	if strings.EqualFold(n1.LocalPath, n2.LocalPath) {
		t.Fatalf("local paths collide: %q and %q", n1.LocalPath, n2.LocalPath)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(n1.LocalPath)), "upper")
	mustRead(t, filepath.Join(root, filepath.FromSlash(n2.LocalPath)), "lower")
	if n2.RelPath != "foo.md" {
		t.Fatalf("server name changed: %q", n2.RelPath)
	}
	copies, _ := filepath.Glob(filepath.Join(root, "*"+localConflictTag+"*"))
	if len(copies) != 0 {
		t.Fatalf("unexpected conflict copies: %v", copies)
	}
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
	// A later update of the second node keeps finding it under the same local name.
	lower2 := []byte("lower v2")
	src.changes = append(src.changes, Change{Seq: 3, NodeID: "n2", RelPath: "foo.md", Version: 2, ContentHash: hashOf(lower2), Size: int64(len(lower2))})
	src.bodies["n2"] = [][]byte{lower2}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(n1.LocalPath)), "upper")
	mustRead(t, filepath.Join(root, filepath.FromSlash(n2.LocalPath)), "lower v2")
}

// A server rename that only changes case keeps the file (it is the same file on a
// case-insensitive disk) and is not mistaken for a collision with itself.
func TestCaseOnlyServerRename(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "foo.md", Version: 1, ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	src.changes = append(src.changes, Change{Seq: 2, NodeID: "n1", RelPath: "Foo.md", Version: 2, ContentHash: hashOf(body), Size: int64(len(body))})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	n, _, _ := e.idx.Get("n1")
	if n.LocalPath != "Foo.md" {
		t.Fatalf("LocalPath = %q, want Foo.md", n.LocalPath)
	}
	mustRead(t, filepath.Join(root, "Foo.md"), "note")
	copies, _ := filepath.Glob(filepath.Join(root, "*"+localConflictTag+"*"))
	if len(copies) != 0 {
		t.Fatalf("unexpected conflict copies: %v", copies)
	}
}

func TestNormalizationVariantServerNamesDoNotCollideOnDisk(t *testing.T) {
	nfc, nfd := "café.md", "café.md"
	a, b := []byte("composed"), []byte("decomposed")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: nfc, Version: 1, ContentHash: hashOf(a), Size: int64(len(a))},
			{Seq: 2, NodeID: "n2", RelPath: nfd, Version: 1, ContentHash: hashOf(b), Size: int64(len(b))},
		},
		bodies: map[string][][]byte{"n1": {a}, "n2": {b}},
	}
	e, root := newEngine(t, src)
	probe := filepath.Join(root, nfc)
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, serr := os.Stat(filepath.Join(root, nfd))
	os.Remove(probe)
	if serr != nil {
		t.Skip("filesystem tells NFC and NFD names apart; they cannot collide here")
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	n1, _, _ := e.idx.Get("n1")
	n2, _, _ := e.idx.Get("n2")
	mustRead(t, filepath.Join(root, n1.LocalPath), "composed")
	mustRead(t, filepath.Join(root, n2.LocalPath), "decomposed")
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// Folders that differ only in case are one folder on a case-insensitive disk. The second
// gets its own local name, and its children must follow it there: placed by their server
// path they would land in the first folder, and the next push would move them into it on
// the server.
func TestCaseVariantFoldersKeepTheirChildrenApart(t *testing.T) {
	a, b := []byte("in Docs"), []byte("in docs")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "Docs", IsDir: true, Version: 1},
			{Seq: 2, NodeID: "d2", RelPath: "docs", IsDir: true, Version: 1},
			{Seq: 3, NodeID: "f1", RelPath: "Docs/a.md", Version: 1, ContentHash: hashOf(a), Size: int64(len(a))},
			{Seq: 4, NodeID: "f2", RelPath: "docs/b.md", Version: 1, ContentHash: hashOf(b), Size: int64(len(b))},
		},
		bodies: map[string][][]byte{"f1": {a}, "f2": {b}},
	}
	e, root := newEngine(t, src)
	if !caseInsensitiveFS(t, root) {
		t.Skip("filesystem is case-sensitive; the folders cannot collide here")
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	d2, _, _ := e.idx.Get("d2")
	f2, _, _ := e.idx.Get("f2")
	if strings.EqualFold(d2.LocalPath, "Docs") {
		t.Fatalf("folder docs was not given its own local name: %q", d2.LocalPath)
	}
	if f2.LocalPath != d2.LocalPath+"/b.md" {
		t.Fatalf("f2 LocalPath = %q, want it inside %q", f2.LocalPath, d2.LocalPath)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(f2.LocalPath)), "in docs")
	mustRead(t, filepath.Join(root, "Docs", "a.md"), "in Docs")
	mustBeGone(t, filepath.Join(root, "Docs", "b.md"))
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
	sink := newFakeSink()
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if len(sink.ops) != 0 {
		t.Fatalf("push sent %v; nothing should go to the server", sink.ops)
	}
	// A later update of the child keeps finding it in its folder.
	b2 := []byte("in docs v2")
	src.changes = append(src.changes, Change{Seq: 5, NodeID: "f2", RelPath: "docs/b.md", Version: 2, ContentHash: hashOf(b2), Size: int64(len(b2))})
	src.bodies["f2"] = [][]byte{b2}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(f2.LocalPath)), "in docs v2")
	mustRead(t, filepath.Join(root, "Docs", "a.md"), "in Docs")

	// Once the clash is gone, a re-sent entry for the folder leaves it where it is:
	// moving it would strand its children's indexed paths.
	src.changes = append(src.changes,
		Change{Seq: 6, NodeID: "f1", RelPath: "Docs/a.md", Deleted: true},
		Change{Seq: 7, NodeID: "d1", RelPath: "Docs", IsDir: true, Deleted: true},
		Change{Seq: 8, NodeID: "d2", RelPath: "docs", IsDir: true, Version: 2},
	)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal after the clash went = %+v, %v; want nothing", got, err)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(f2.LocalPath)), "in docs v2")
}

// An unsynced file sitting where the server moves a folder is kept as a local conflict copy,
// and the folder with its contents lands in its place.
func TestServerDirRenameKeepsUnsyncedFileAtTarget(t *testing.T) {
	body := []byte("inside")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "a", IsDir: true},
			{Seq: 2, NodeID: "n1", RelPath: "a/f.txt", ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	writeAt(t, filepath.Join(root, "b"), "local file named b")
	src.changes = append(src.changes,
		Change{Seq: 3, NodeID: "d1", RelPath: "b", IsDir: true},
		Change{Seq: 4, NodeID: "n1", RelPath: "b/f.txt", ContentHash: hashOf(body), Size: int64(len(body))},
	)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	mustRead(t, filepath.Join(root, "b", "f.txt"), "inside")
	mustBeGone(t, filepath.Join(root, "a"))
	copies, _ := filepath.Glob(filepath.Join(root, "b"+localConflictTag+"*)"))
	if len(copies) != 1 {
		t.Fatalf("want one local conflict copy of b, got %v", copies)
	}
	mustRead(t, copies[0], "local file named b")
	if got, err := e.DetectLocal(); err != nil || len(got) != 1 || got[0].Op != "create" {
		t.Fatalf("DetectLocal = %+v, %v; want only the conflict copy as a new file", got, err)
	}
}

// A server rename of a folder that only changes its case must change it on disk too:
// the target resolves to the folder itself, which is not "a folder already there".
func TestCaseOnlyServerFolderRename(t *testing.T) {
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "Notes", IsDir: true, Version: 1},
			{Seq: 2, NodeID: "n1", RelPath: "Notes/a.md", Version: 1, ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if !caseInsensitiveFS(t, root) {
		t.Skip("filesystem is case-sensitive; a case-only rename is an ordinary one here")
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	src.changes = append(src.changes,
		Change{Seq: 3, NodeID: "d1", RelPath: "notes", IsDir: true, Version: 2},
		Change{Seq: 4, NodeID: "n1", RelPath: "notes/a.md", Version: 2, ContentHash: hashOf(body), Size: int64(len(body))},
	)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "notes" {
		t.Fatalf("root entries = %v, %v; want [notes]", entries, err)
	}
	mustRead(t, filepath.Join(root, "notes", "a.md"), "note")
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// A server name can be exactly the local name generated for another node; the newcomer
// must not land on that node's file.
func TestServerNameEqualToGeneratedLocalName(t *testing.T) {
	upper, lower, third := []byte("upper"), []byte("lower"), []byte("third")
	generated := localname.Disambiguate("foo.md", "n2")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "Foo.md", Version: 1, ContentHash: hashOf(upper), Size: int64(len(upper))},
			{Seq: 2, NodeID: "n2", RelPath: "foo.md", Version: 1, ContentHash: hashOf(lower), Size: int64(len(lower))},
			{Seq: 3, NodeID: "n3", RelPath: generated, Version: 1, ContentHash: hashOf(third), Size: int64(len(third))},
		},
		bodies: map[string][][]byte{"n1": {upper}, "n2": {lower}, "n3": {third}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	n2, _, _ := e.idx.Get("n2")
	n3, _, _ := e.idx.Get("n3")
	if n2.LocalPath != generated {
		t.Skipf("n2 was not disambiguated here (LocalPath %q); nothing to collide with", n2.LocalPath)
	}
	if n3.LocalPath == n2.LocalPath {
		t.Fatalf("n3 placed on n2's file %q", n3.LocalPath)
	}
	mustRead(t, filepath.Join(root, n2.LocalPath), "lower")
	mustRead(t, filepath.Join(root, n3.LocalPath), "third")
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

// A child placed by the fallback because its folder's entry failed moves into the folder
// once the folder is applied under its own local name.
func TestChildPlacedBeforeItsFolderFollowsItLater(t *testing.T) {
	a, b := []byte("in Docs"), []byte("in docs")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "Docs", IsDir: true, Version: 1},
			{Seq: 2, NodeID: "d2", RelPath: "docs", IsDir: true, Version: 1},
			{Seq: 3, NodeID: "f1", RelPath: "Docs/a.md", Version: 1, ContentHash: hashOf(a), Size: int64(len(a))},
			{Seq: 4, NodeID: "f2", RelPath: "docs/b.md", Version: 1, ContentHash: hashOf(b), Size: int64(len(b))},
		},
		bodies: map[string][][]byte{"f1": {a}, "f2": {b, b}},
	}
	e, root := newEngine(t, src)
	if !caseInsensitiveFS(t, root) {
		t.Skip("filesystem is case-sensitive; the folders cannot collide here")
	}
	// A file where d2's local folder goes makes its entry fail on the first pull. The
	// mirror counts as established, so the folder is not set aside for holding it.
	if err := e.idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, localname.Disambiguate("docs", "d2"))
	writeAt(t, blocker, "in the way")
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatal("expected d2's entry to fail")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	d2, _, _ := e.idx.Get("d2")
	f2, _, _ := e.idx.Get("f2")
	if f2.LocalPath != d2.LocalPath+"/b.md" {
		t.Fatalf("f2 LocalPath = %q, want it inside %q", f2.LocalPath, d2.LocalPath)
	}
	mustRead(t, filepath.Join(root, filepath.FromSlash(f2.LocalPath)), "in docs")
	mustBeGone(t, filepath.Join(root, "Docs", "b.md"))
	if got, err := e.DetectLocal(); err != nil || len(got) != 0 {
		t.Fatalf("DetectLocal = %+v, %v; want nothing", got, err)
	}
}

func TestOnlyPushFailuresRejectsJoinedErrors(t *testing.T) {
	pf := &PushFailures{err: errors.New("a.bin: 413")}
	if !OnlyPushFailures(pf) {
		t.Fatal("a bare *PushFailures must count as a finished pass")
	}
	if OnlyPushFailures(errors.Join(pf, errors.New("pull: connection refused"))) {
		t.Fatal("a failed pull joined to push failures must not count as a finished pass")
	}
	if OnlyPushFailures(nil) || OnlyPushFailures(errors.New("x")) {
		t.Fatal("nil or an unrelated error must not count")
	}
}
