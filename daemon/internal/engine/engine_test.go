package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"discodrive.org/daemon/internal/index"
)

type fakeSource struct {
	changes  []Change
	bodies   map[string][][]byte // per-nodeID download queue (one body consumed per download)
	failOn   string
	pageSize int
}

func (f *fakeSource) Changes(_ context.Context, since int64, _ int) ([]Change, int64, bool, error) {
	var out []Change
	for _, c := range f.changes {
		if c.Seq > since {
			out = append(out, c)
		}
	}
	if f.pageSize > 0 && len(out) > f.pageSize {
		page := out[:f.pageSize]
		return page, page[len(page)-1].Seq, true, nil
	}
	cursor := since
	if len(out) > 0 {
		cursor = out[len(out)-1].Seq
	}
	return out, cursor, false, nil
}

func (f *fakeSource) Download(_ context.Context, nodeID string, w io.Writer) error {
	if nodeID == f.failOn {
		return errors.New("download interrupted")
	}
	q := f.bodies[nodeID]
	if len(q) == 0 {
		return errors.New("no content")
	}
	b := q[0]
	f.bodies[nodeID] = q[1:]
	_, err := w.Write(b)
	return err
}

type countingSource struct {
	fakeSource
	calls *int
}

func (c *countingSource) Download(ctx context.Context, nodeID string, w io.Writer) error {
	*c.calls++
	return c.fakeSource.Download(ctx, nodeID, w)
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newEngine(t *testing.T, src Source) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return New(src, idx, root), root
}

func TestApplyCreateFile(t *testing.T) {
	body := []byte("hello")
	src := &fakeSource{
		changes: []Change{{Seq: 1, Op: "create", NodeID: "n1", RelPath: "dir/a.txt", ContentHash: hashOf(body), Size: int64(len(body))}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "dir", "a.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("file: %q err=%v", got, err)
	}
}

func TestApplyCreateDir(t *testing.T) {
	src := &fakeSource{changes: []Change{{Seq: 1, Op: "create", NodeID: "d1", RelPath: "sub", IsDir: true}}}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("folder was not created: %v", err)
	}
}

func TestApplyUpdateOverwrites(t *testing.T) {
	v1, v2 := []byte("one"), []byte("two")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(v1), Size: 3},
			{Seq: 2, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(v2), Size: 3},
		},
		bodies: map[string][][]byte{"n1": {v1, v2}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "two" {
		t.Fatalf("expected two, got %q", got)
	}
}

func TestApplyNoOpWhenHashUnchanged(t *testing.T) {
	body := []byte("same")
	calls := 0
	src := &countingSource{fakeSource: fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(body), Size: 4},
			{Seq: 2, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(body), Size: 4},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}, calls: &calls}
	e, _ := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Download called %d times, expected 1 (second change with the same hash — no download)", calls)
	}
}

func TestApplyMove(t *testing.T) {
	body := []byte("x")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "old.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 2, Op: "move", NodeID: "n1", RelPath: "new.txt", ContentHash: hashOf(body), Size: 1},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old path must disappear")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(got) != "x" {
		t.Fatalf("new path: %q", got)
	}
}

func TestApplyDirRenameMovesSubtree(t *testing.T) {
	body := []byte("content")
	calls := 0
	src := &countingSource{fakeSource: fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "d1", RelPath: "docs", IsDir: true},
			{Seq: 2, Op: "create", NodeID: "n1", RelPath: "docs/a.txt", ContentHash: hashOf(body), Size: int64(len(body))},
			// Server-side folder rename: the feed now carries one event per live
			// descendant (parents before children), all with op=move.
			{Seq: 3, Op: "move", NodeID: "d1", RelPath: "archive", IsDir: true},
			{Seq: 4, Op: "move", NodeID: "n1", RelPath: "archive/a.txt", ContentHash: hashOf(body), Size: int64(len(body))},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}, calls: &calls}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs")); !os.IsNotExist(err) {
		t.Fatalf("old dir must be gone after rename, stat err=%v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, "archive")); err != nil || !fi.IsDir() {
		t.Fatalf("new dir must exist: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "archive", "a.txt")); string(got) != "content" {
		t.Fatalf("file must be inside the new dir, got %q", got)
	}
	if calls != 1 {
		t.Fatalf("Download called %d times, expected 1 (rename must not re-download)", calls)
	}
}

func TestApplyDirRenameLeavesNoGhostForDetect(t *testing.T) {
	body := []byte("g")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "d1", RelPath: "docs", IsDir: true},
			{Seq: 2, Op: "create", NodeID: "n1", RelPath: "docs/a.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 3, Op: "move", NodeID: "d1", RelPath: "archive", IsDir: true},
			{Seq: 4, Op: "move", NodeID: "n1", RelPath: "archive/a.txt", ContentHash: hashOf(body), Size: 1},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, _ := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Regression: the old dir used to survive on disk, so DetectLocal reported it as
	// a local create and PushLocal resurrected an empty ghost folder on the server.
	got, err := e.DetectLocal()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("DetectLocal after dir rename must be empty, got %+v", got)
	}
}

func TestApplyChildEventsAfterDirRenameAreNoOps(t *testing.T) {
	body := []byte("child")
	calls := 0
	src := &countingSource{fakeSource: fakeSource{
		changes: []Change{
			{Seq: 1, Op: "create", NodeID: "d1", RelPath: "docs", IsDir: true},
			{Seq: 2, Op: "create", NodeID: "n1", RelPath: "docs/a.txt", ContentHash: hashOf(body), Size: int64(len(body))},
			{Seq: 3, Op: "move", NodeID: "d1", RelPath: "archive", IsDir: true},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}, calls: &calls}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The child's own move event arrives after the dir was already renamed on disk:
	// the file is at the new path with the same hash — must be an index-only update.
	src.changes = append(src.changes,
		Change{Seq: 4, Op: "move", NodeID: "n1", RelPath: "archive/a.txt", ContentHash: hashOf(body), Size: int64(len(body))})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("Download called %d times, expected 1 (child already in place)", calls)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "archive", "a.txt")); string(got) != "child" {
		t.Fatalf("child file: %q", got)
	}
	if n, ok, _ := e.idx.Get("n1"); !ok || n.RelPath != "archive/a.txt" {
		t.Fatalf("index must track the new child path, got %+v ok=%v", n, ok)
	}
}

func TestApplyDelete(t *testing.T) {
	body := []byte("x")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 2, Op: "delete", NodeID: "n1", RelPath: "a.txt", Deleted: true},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("file must be deleted")
	}
}

func TestResumeAfterDownloadFailure(t *testing.T) {
	b1, b3 := []byte("one"), []byte("three")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "1.txt", ContentHash: hashOf(b1), Size: 3},
			{Seq: 2, NodeID: "n2", RelPath: "2.txt", ContentHash: hashOf([]byte("two")), Size: 3},
			{Seq: 3, NodeID: "n3", RelPath: "3.txt", ContentHash: hashOf(b3), Size: 5},
		},
		bodies: map[string][][]byte{"n1": {b1}, "n2": {[]byte("two")}, "n3": {b3}},
		failOn: "n2",
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatalf("expected an error at n2")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "1.txt")); string(got) != "one" {
		t.Fatalf("1.txt must be applied before the interruption")
	}
	if _, err := os.Stat(filepath.Join(root, "3.txt")); !os.IsNotExist(err) {
		t.Fatalf("3.txt must not exist after the interruption")
	}
	src.failOn = ""
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "2.txt")); string(got) != "two" {
		t.Fatalf("2.txt after resume: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "3.txt")); string(got) != "three" {
		t.Fatalf("3.txt after resume: %q", got)
	}
}

func TestPagination(t *testing.T) {
	body := []byte("p")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "1.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 2, NodeID: "n2", RelPath: "2.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 3, NodeID: "n3", RelPath: "3.txt", ContentHash: hashOf(body), Size: 1},
		},
		bodies:   map[string][][]byte{"n1": {body}, "n2": {body}, "n3": {body}},
		pageSize: 2,
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1.txt", "2.txt", "3.txt"} {
		if _, err := os.Stat(filepath.Join(root, n)); err != nil {
			t.Fatalf("%s not applied (pagination): %v", n, err)
		}
	}
}

func TestRejectsRootDeletion(t *testing.T) {
	body := []byte("keep")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "n1", RelPath: "keep.txt", ContentHash: hashOf(body), Size: 4},
			// A malicious server tries to wipe the whole sync folder with an empty rel_path.
			{Seq: 2, NodeID: "root", RelPath: "", Deleted: true},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	// The bad delete must surface an error, not silently wipe the root.
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatalf("expected rejection of root deletion")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("sync root must survive an empty-rel_path delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Fatalf("existing file must survive: %v", err)
	}
}

func TestRejectsPathEscape(t *testing.T) {
	body := []byte("evil")
	src := &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "../evil.txt", ContentHash: hashOf(body), Size: 4}},
		bodies:  map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err == nil {
		t.Fatalf("expected rejection on path escape")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("a file outside the root must not be created")
	}
}

// A purged node is re-announced as a delete with its ORIGINAL path, after a new folder may
// have taken that path. The delete goes by id: the old node is no longer indexed, so it is a
// no-op, and the new folder's files stay on disk and in the index.
func TestPurgeOfAnOldNodeKeepsTheNewNodeAtItsPath(t *testing.T) {
	body := []byte("new")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "A", RelPath: "P", IsDir: true},
			{Seq: 2, Op: "delete", NodeID: "A", RelPath: "P", IsDir: true, Deleted: true},
			{Seq: 3, NodeID: "B", RelPath: "P", IsDir: true},
			{Seq: 4, NodeID: "f", RelPath: "P/f", ContentHash: hashOf(body), Size: 3},
			{Seq: 5, Op: "delete", NodeID: "A", RelPath: "P", IsDir: true, Deleted: true},
		},
		bodies: map[string][][]byte{"f": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "P", "f")); err != nil || string(got) != "new" {
		t.Fatalf("P/f of the new folder must stay: %q %v", got, err)
	}
	for _, id := range []string{"B", "f"} {
		if _, ok, _ := e.idx.Get(id); !ok {
			t.Errorf("%s must stay in the index", id)
		}
	}
}
