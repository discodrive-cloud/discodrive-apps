package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/localname"
)

// contents maps every regular file under root (relative, slash-separated) to its body.
func contents(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			b, _ := os.ReadFile(p)
			out[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return out
}

func conflictCopies(files map[string]string) map[string]string {
	out := map[string]string{}
	for p, b := range files {
		if strings.Contains(p, "(conflict, local, ") {
			out[p] = b
		}
	}
	return out
}

func oneFileServer(body string) *fakeSource {
	return &fakeSource{
		changes: []Change{{Seq: 1, NodeID: "n1", RelPath: "a.txt", ContentHash: hashOf([]byte(body)), Size: int64(len(body)), Version: 1}},
		bodies:  map[string][][]byte{"n1": {[]byte(body)}},
	}
}

func serverEdit(src *fakeSource, seq int64, body string) {
	src.changes = append(src.changes, Change{Seq: seq, NodeID: "n1", RelPath: "a.txt",
		ContentHash: hashOf([]byte(body)), Size: int64(len(body)), Version: seq})
	src.bodies["n1"] = append(src.bodies["n1"], []byte(body))
}

// "Keep local files" reset: a file identical to the server's is indexed without a
// download, and one edited locally is never silently replaced.
func TestKeepLocalResetNeverOverwritesEdits(t *testing.T) {
	calls := 0
	src := &countingSource{fakeSource: *oneFileServer("server"), calls: &calls}
	src.changes = append(src.changes, Change{Seq: 2, NodeID: "n2", RelPath: "same.txt",
		ContentHash: hashOf([]byte("same")), Size: 4, Version: 1})
	src.bodies["n2"] = [][]byte{[]byte("same")}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "a.txt", "local edit")
	if err := e.ResetIndexKeepingFiles(); err != nil {
		t.Fatal(err)
	}
	src.bodies["n1"] = [][]byte{[]byte("server")}
	calls = 0
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := contents(t, root)
	if files["a.txt"] != "server" || files["same.txt"] != "same" {
		t.Fatalf("tree after reset: %v", files)
	}
	if cc := conflictCopies(files); len(cc) != 1 {
		t.Fatalf("the local edit must survive as one conflict copy, got %v", files)
	} else {
		for _, b := range cc {
			if b != "local edit" {
				t.Fatalf("conflict copy holds %q", b)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("downloads = %d, want 1 (same.txt already matches)", calls)
	}
}

// A server update arriving before an unpushed local edit went up must not replace it.
func TestPullKeepsUnpushedEditAsConflictCopy(t *testing.T) {
	src := oneFileServer("v1")
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "a.txt", "mine")
	serverEdit(src, 2, "v2")
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := contents(t, root)
	if files["a.txt"] != "v2" {
		t.Fatalf("a.txt = %q", files["a.txt"])
	}
	if cc := conflictCopies(files); len(cc) != 1 {
		t.Fatalf("want one conflict copy with the edit, got %v", files)
	}
	// The copy is a new local file: the next push uploads it.
	changes, err := e.DetectLocal()
	if err != nil || len(changes) != 1 || changes[0].Op != "create" {
		t.Fatalf("DetectLocal = %+v, %v", changes, err)
	}
}

// A server-side delete must not remove a local file with unpushed edits, nor files
// in a deleted folder that never reached the server.
func TestServerDeleteKeepsUnsyncedData(t *testing.T) {
	body := []byte("x")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "dir", IsDir: true},
			{Seq: 2, NodeID: "n1", RelPath: "dir/x.txt", ContentHash: hashOf(body), Size: 1},
			{Seq: 3, NodeID: "n2", RelPath: "a.txt", ContentHash: hashOf(body), Size: 1},
		},
		bodies: map[string][][]byte{"n1": {body}, "n2": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "a.txt", "edited")
	mustWrite(t, root, "dir/new.txt", "never synced")
	src.changes = append(src.changes,
		Change{Seq: 4, NodeID: "n2", RelPath: "a.txt", Deleted: true},
		Change{Seq: 5, NodeID: "d1", RelPath: "dir", IsDir: true, Deleted: true})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := contents(t, root)
	if files["a.txt"] != "edited" || files["dir/new.txt"] != "never synced" {
		t.Fatalf("unsynced data lost: %v", files)
	}
	if _, ok := files["dir/x.txt"]; ok {
		t.Fatalf("the synced, unchanged file must follow the server delete")
	}
	changes, _ := e.DetectLocal()
	creates := 0
	for _, c := range changes {
		if c.Op == "create" {
			creates++
		}
		if c.Op == "delete" {
			t.Fatalf("stale index entry produces a delete: %+v", c)
		}
	}
	if creates < 2 {
		t.Fatalf("kept files must go up again, got %+v", changes)
	}
}

// Editing a file whose server name was localised must push from its local path under
// the server name, keeping the local name in the index.
func TestPushEditOfLocalisedFile(t *testing.T) {
	localname.RestrictForTest(t)
	body := []byte("note")
	src := &fakeSource{
		changes: []Change{
			{Seq: 1, NodeID: "d1", RelPath: "notes", IsDir: true},
			{Seq: 2, NodeID: "n1", RelPath: "notes/worth it?.md", ContentHash: hashOf(body), Size: 4, Version: 1},
		},
		bodies: map[string][][]byte{"n1": {body}},
	}
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	local := localname.Localize("notes/worth it?.md")
	mustWrite(t, root, local, "edited note")
	sink := &sameNodeSink{fakeSink: newFakeSink(), id: "n1"}
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatalf("PushLocal: %v", err)
	}
	if _, ok := sink.pushed["notes/worth it?.md"]; !ok {
		t.Fatalf("pushed %v, want the server name", sink.pushed)
	}
	n, ok, _ := e.idx.GetByPath("notes/worth it?.md")
	if !ok || n.LocalPath != local || n.ContentHash != hashOf([]byte("edited note")) {
		t.Fatalf("index after push: %+v ok=%v", n, ok)
	}
	if changes, _ := e.DetectLocal(); len(changes) != 0 {
		t.Fatalf("changes after push: %+v", changes)
	}
}

// sameNodeSink answers an update the way the server does: same node, next version.
type sameNodeSink struct {
	*fakeSink
	id string
}

func (s *sameNodeSink) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, mod time.Time) (RemoteNode, bool, error) {
	rn, c, err := s.fakeSink.PushFile(ctx, rel, base, r, mod)
	rn.NodeID = s.id
	return rn, c, err
}

// An upload torn by a concurrent edit: the server echoes the pushed version with a
// hash that is not the file's. The pull keeps the file; the next push resends it.
func TestTornUploadIsResent(t *testing.T) {
	src := oneFileServer("v1")
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "a.txt", "complete edit")
	sink := &sameNodeSink{fakeSink: newFakeSink(), id: "n1"}
	if err := e.PushLocal(context.Background(), sink); err != nil {
		t.Fatal(err)
	}
	n, _, _ := e.idx.Get("n1")
	// The server stored a torn mix under the pushed version.
	src.changes = append(src.changes, Change{Seq: 2, NodeID: "n1", RelPath: "a.txt",
		ContentHash: hashOf([]byte("torn")), Size: 4, Version: n.Version})
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	files := contents(t, root)
	if files["a.txt"] != "complete edit" || len(conflictCopies(files)) != 0 {
		t.Fatalf("tree after echo: %v", files)
	}
	changes, _ := e.DetectLocal()
	if len(changes) != 1 || changes[0].Op != "update" {
		t.Fatalf("the torn upload must be resent, got %+v", changes)
	}
}

// A scope change resets the index and sweeps orphans; the conflict copies its pull
// just made of local edits are the user's data and must survive the sweep.
func TestScopeSweepKeepsLocalConflictCopies(t *testing.T) {
	src := oneFileServer("server")
	src.changes[0].RelPath = "dir/a.txt"
	src.changes = append([]Change{{Seq: 0, NodeID: "d", RelPath: "dir", IsDir: true}}, src.changes...)
	e, root := newEngine(t, src)
	if err := e.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, root, "dir/a.txt", "local edit")
	src.bodies["n1"] = [][]byte{[]byte("server")}
	if err := e.ResetForScope(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	cc := conflictCopies(contents(t, root))
	if len(cc) != 1 {
		t.Fatalf("conflict copy swept: %v", contents(t, root))
	}
}

func TestPullPreservesLongNamedLocalConflict(t *testing.T) {
	for _, name := range []string{strings.Repeat("a", 240) + ".txt", strings.Repeat("я", 120) + ".txt"} {
		t.Run(name[:8], func(t *testing.T) {
			src := oneFileServer("server")
			src.changes[0].RelPath = name
			e, root := newEngine(t, src)
			if err := e.PullOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, root, name, "local edit")
			src.changes = append(src.changes, Change{Seq: 2, NodeID: "n1", RelPath: name, ContentHash: hashOf([]byte("new server")), Size: 10, Version: 2})
			src.bodies["n1"] = append(src.bodies["n1"], []byte("new server"))
			done := make(chan error, 1)
			go func() { done <- e.PullOnce(context.Background()) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("conflict naming did not terminate")
			}
			files := contents(t, root)
			if files[name] != "new server" {
				t.Fatal("server file missing")
			}
			copies := conflictCopies(files)
			if len(copies) != 1 {
				t.Fatalf("conflict copies: %v", copies)
			}
			for _, body := range copies {
				if body != "local edit" {
					t.Fatal("local edit lost")
				}
			}
		})
	}
}

func TestConflictNameReturnsFilesystemError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "not-a-directory", "file")
	if _, err := localConflictName(filepath.Join(root, "not-a-directory", "child.txt")); err == nil {
		t.Fatal("expected an error for a non-directory parent")
	}
}
