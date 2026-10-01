package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/localname"
)

// Round 10 (external review): a delete never takes server content whose move or upload
// failed in the same pass, and a new local file in a folder shown under a generated name
// goes up under the folder's server name.

// failingSink fails MoveNode for the given node ids and PushFile for the given paths.
type failingSink struct {
	*fakeSink
	moveErr map[string]error
	pushErr map[string]error
}

func (s failingSink) MoveNode(ctx context.Context, nodeID, parent string) error {
	if err := s.moveErr[nodeID]; err != nil {
		return err
	}
	return s.fakeSink.MoveNode(ctx, nodeID, parent)
}

func (s failingSink) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, m time.Time) (RemoteNode, bool, error) {
	if err := s.pushErr[rel]; err != nil {
		return RemoteNode{}, false, err
	}
	return s.fakeSink.PushFile(ctx, rel, base, r, m)
}

// noMoveSink hides MoveNode/RenameNode: a rename is a create plus a delete.
type noMoveSink struct{ s failingSink }

func (n noMoveSink) PushFile(ctx context.Context, rel string, base *int64, r io.Reader, m time.Time) (RemoteNode, bool, error) {
	return n.s.PushFile(ctx, rel, base, r, m)
}
func (n noMoveSink) EnsureDir(ctx context.Context, rel string) (RemoteNode, error) {
	return n.s.EnsureDir(ctx, rel)
}
func (n noMoveSink) DeleteRemote(ctx context.Context, rel string) error {
	return n.s.DeleteRemote(ctx, rel)
}

func deletedOn(s *fakeSink, rel string) bool {
	for _, d := range s.deleted {
		if d == rel {
			return true
		}
	}
	return false
}

// renamedFolderFixture: server folder old/ with old/sub/f.md and other.md at the root; the
// user renames old to new and deletes other.md.
func renamedFolderFixture(t *testing.T) (*Engine, string) {
	t.Helper()
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "old", LocalPath: "old", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "S", RelPath: "old/sub", LocalPath: "old/sub", IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "F", RelPath: "old/sub/f.md", LocalPath: "old/sub/f.md", Version: 1}, seq: 3, content: "f"},
		{n: index.Node{NodeID: "G", RelPath: "old/g.md", LocalPath: "old/g.md", Version: 1}, seq: 3, content: "g"},
		{n: index.Node{NodeID: "O", RelPath: "other.md", LocalPath: "other.md", Version: 1}, seq: 4, content: "other"},
	})
	if err := os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "other.md")); err != nil {
		t.Fatal(err)
	}
	return e, root
}

// P1: a failed move (409, or any other error) of a file inside a renamed folder keeps the
// old folder, and every folder above that file, on the server for the next pass. A delete
// that does not depend on it still goes.
func TestFailedMoveKeepsItsOldFolder(t *testing.T) {
	for _, moveErr := range []error{errors.New("PATCH /files/F/move: 409: conflict"), errors.New("boom")} {
		e, _ := renamedFolderFixture(t)
		s := failingSink{newFakeSink(), map[string]error{"F": moveErr}, nil}
		err := e.PushLocal(context.Background(), s)
		if !OnlyPushFailures(err) {
			t.Fatalf("PushLocal = %v, want the move's failure", err)
		}
		for _, rel := range []string{"old", "old/sub", "old/sub/f.md"} {
			if deletedOn(s.fakeSink, rel) {
				t.Errorf("%v: %s deleted on the server with F still in it (ops %v)", moveErr, rel, s.ops)
			}
		}
		if !deletedOn(s.fakeSink, "other.md") {
			t.Errorf("independent delete held back (ops %v)", s.ops)
		}
		wantRow(t, e, "F", true)
		wantRow(t, e, "D", true)
		// The next pass, with the server answering again, finishes the rename.
		s.moveErr = nil
		if err := e.PushLocal(context.Background(), s); err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if !deletedOn(s.fakeSink, "old") {
			t.Errorf("second pass did not delete the old folder (ops %v)", s.ops)
		}
		wantNoLocalChanges(t, e)
	}
}

// The same without server-side moves: a rename is an upload plus a delete, and a failed
// upload keeps the old copy (and its folders) on the server.
func TestFailedUploadKeepsTheOldCopy(t *testing.T) {
	e, _ := renamedFolderFixture(t)
	fs := failingSink{newFakeSink(), nil, map[string]error{"new/sub/f.md": errors.New("PUT: 409: conflict")}}
	err := e.PushLocal(context.Background(), noMoveSink{fs})
	if !OnlyPushFailures(err) {
		t.Fatalf("PushLocal = %v, want the upload's failure", err)
	}
	for _, rel := range []string{"old", "old/sub", "old/sub/f.md"} {
		if deletedOn(fs.fakeSink, rel) {
			t.Errorf("%s deleted with the only uploaded copy failed (ops %v)", rel, fs.ops)
		}
	}
	if !deletedOn(fs.fakeSink, "old/g.md") || !deletedOn(fs.fakeSink, "other.md") {
		t.Errorf("independent deletes held back (ops %v)", fs.ops)
	}
}

// P2: server folder "foo" is shown as foo~<hash> next to "FOO". A new file in it goes up as
// foo/new.txt, and stays put afterwards.
func genFolderFixture(t *testing.T) (*Engine, string, string) {
	t.Helper()
	fl := localname.Disambiguate("foo", "B")
	bl := localname.Disambiguate(fl+"/bar", "C")
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "A", RelPath: "FOO", LocalPath: "FOO", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "B", RelPath: "foo", LocalPath: fl, IsDir: true, Version: 1}, seq: 2},
		{n: index.Node{NodeID: "Bb", RelPath: "foo/BAR", LocalPath: fl + "/BAR", IsDir: true, Version: 1}, seq: 3},
		{n: index.Node{NodeID: "C", RelPath: "foo/bar", LocalPath: bl, IsDir: true, Version: 1}, seq: 4},
		{n: index.Node{NodeID: "M", RelPath: "a.md", LocalPath: "a.md", Version: 1}, seq: 5, content: "a"},
	})
	return e, root, fl
}

func TestNewFileInGeneratedFolderUsesServerName(t *testing.T) {
	e, root, fl := genFolderFixture(t)
	bl := localname.Disambiguate(fl+"/bar", "C")
	writeAt(t, filepath.Join(root, fl, "new.txt"), "new")
	writeAt(t, filepath.Join(root, fl, "sub", "x.txt"), "x")
	writeAt(t, filepath.Join(root, bl, "n.txt"), "nested")
	s := newFakeSink()
	if err := e.PushLocal(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"foo/new.txt", "foo/sub/x.txt", "foo/bar/n.txt"} {
		if _, ok := s.pushed[want]; !ok {
			t.Errorf("%s not pushed (ops %v)", want, s.ops)
		}
	}
	for _, d := range s.dirs {
		if d != "foo/sub" {
			t.Errorf("EnsureDir(%s), want only foo/sub", d)
		}
	}
	wantLocal(t, e, "srv-foo/new.txt", fl+"/new.txt")
	wantLocal(t, e, "dir-foo/sub", fl+"/sub")
	wantNoLocalChanges(t, e)
}

// A move into such a folder goes to the folder by its server name.
func TestMoveIntoGeneratedFolderUsesServerName(t *testing.T) {
	e, root, fl := genFolderFixture(t)
	if err := os.Rename(filepath.Join(root, "a.md"), filepath.Join(root, fl, "a.md")); err != nil {
		t.Fatal(err)
	}
	s := newFakeSink()
	if err := e.PushLocal(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.moved) != 1 || s.moved[0] != [2]string{"M", "B"} || len(s.deleted) != 0 || len(s.pushed) != 0 {
		t.Errorf("ops %v (moved %v), want M moved into B only", s.ops, s.moved)
	}
	n, _, _ := e.idx.Get("M")
	if n.RelPath != "foo/a.md" || n.LocalPath != fl+"/a.md" {
		t.Errorf("M = %+v", n)
	}
	wantNoLocalChanges(t, e)
}

// Round 11.

// I-1 (probe R10 EditedFileInRenamedFolderUploadFails): the user renames old to new and
// edits new/sub/f.md, so it goes up as a new file; that upload fails. Its source — the
// server copy at old/sub/f.md, found through the rename the other files' moves show — and
// the folders above it stay until the next pass.
func TestEditedFileInRenamedFolderUploadFailsHoldsOld(t *testing.T) {
	e, root := renamedFolderFixture(t)
	writeAt(t, filepath.Join(root, "new", "sub", "f.md"), "f edited")
	s := failingSink{newFakeSink(), nil, map[string]error{"new/sub/f.md": errors.New("PUT: 507: quota")}}
	if err := e.PushLocal(context.Background(), s); !OnlyPushFailures(err) {
		t.Fatalf("PushLocal = %v", err)
	}
	for _, rel := range []string{"old", "old/sub", "old/sub/f.md"} {
		if deletedOn(s.fakeSink, rel) {
			t.Errorf("%s deleted although the edited copy's upload failed (ops %v)", rel, s.ops)
		}
	}
	if !deletedOn(s.fakeSink, "other.md") || len(s.moved) != 1 {
		t.Errorf("independent changes held back (ops %v)", s.ops)
	}
	s.pushErr = nil
	if err := e.PushLocal(context.Background(), s); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if !deletedOn(s.fakeSink, "old") {
		t.Errorf("second pass did not finish (ops %v)", s.ops)
	}
	wantNoLocalChanges(t, e)
}

// The same when the edited file is all the renamed folder holds: no move shows the rename,
// and the server copy with the same name is held.
func TestEditedOnlyFileInRenamedFolderUploadFailsHoldsOld(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "D", RelPath: "old", LocalPath: "old", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "F", RelPath: "old/f.md", LocalPath: "old/f.md", Version: 1}, seq: 2, content: "f"},
	})
	if err := os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "new", "f.md"), "f edited")
	s := failingSink{newFakeSink(), nil, map[string]error{"new/f.md": errors.New("PUT: 507: quota")}}
	if err := e.PushLocal(context.Background(), s); !OnlyPushFailures(err) {
		t.Fatalf("PushLocal = %v", err)
	}
	for _, rel := range []string{"old", "old/f.md"} {
		if deletedOn(s.fakeSink, rel) {
			t.Errorf("%s deleted (ops %v)", rel, s.ops)
		}
	}
}

// Probe R10 PermanentMoveFailure: only what the failing move needs is held, on every pass.
func TestPermanentMoveFailureHoldsOnlyItsFolders(t *testing.T) {
	e, _ := renamedFolderFixture(t)
	s := failingSink{newFakeSink(), map[string]error{"F": errors.New("409")}, nil}
	for i := 0; i < 3; i++ {
		if err := e.PushLocal(context.Background(), s); !OnlyPushFailures(err) {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	for _, rel := range []string{"old", "old/sub", "old/sub/f.md"} {
		if deletedOn(s.fakeSink, rel) {
			t.Errorf("%s deleted (ops %v)", rel, s.ops)
		}
	}
	if !deletedOn(s.fakeSink, "other.md") {
		t.Errorf("other.md held (ops %v)", s.ops)
	}
	wantRow(t, e, "G", true)
	if n, _, _ := e.idx.Get("G"); n.RelPath != "new/g.md" {
		t.Errorf("G = %+v, want moved", n)
	}
}

// Nit: a failed upload of an empty file does not hold deletes of other empty files (all
// empty files share one hash).
func TestEmptyFileUploadFailureHoldsNoOtherEmptyFile(t *testing.T) {
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "A", RelPath: "a.txt", LocalPath: "a.txt", Version: 1, ContentHash: hashOf(nil)}, seq: 1},
	})
	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "e.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := failingSink{newFakeSink(), nil, map[string]error{"e.txt": errors.New("PUT: 400")}}
	_ = e.PushLocal(context.Background(), noMoveSink{fs})
	if !deletedOn(fs.fakeSink, "a.txt") {
		t.Errorf("a.txt held by the empty hash (ops %v)", fs.ops)
	}
}

// M-1 (probe R10 GeneratedFolderOps): renaming a folder whose subfolder has a generated
// name keeps the subfolder's server name; no "bar~<hash>" folder reaches the server.
func TestRenamedFolderKeepsGeneratedChildServerName(t *testing.T) {
	e, root, fl := genFolderFixture(t)
	bl := localname.Disambiguate(fl+"/bar", "C")
	writeAt(t, filepath.Join(root, bl, "n.md"), "n")
	if err := e.PushLocal(context.Background(), newFakeSink()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, fl), filepath.Join(root, "baz")); err != nil {
		t.Fatal(err)
	}
	s := newFakeSink()
	if err := e.PushLocal(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for _, d := range s.dirs {
		if strings.Contains(d, "~") {
			t.Errorf("EnsureDir(%s): a generated name reached the server (ops %v)", d, s.ops)
		}
	}
	gen := path.Base(bl)
	n, ok, _ := e.idx.Get("dir-baz/bar")
	if !ok || n.LocalPath != "baz/"+gen {
		t.Errorf("baz/bar row = %+v (%v), want local baz/%s (ops %v)", n, ok, gen, s.ops)
	}
	wantNoLocalChanges(t, e)
}

// Round 12.

// m-1 (probe R11): a new file the server rejects for good (413 too large) is not a moved
// copy waiting to go up: deleting a folder that holds another file of the same name is not
// held by it. A temporary rejection (409) still holds, and a held delete is logged.
func TestPermanentRejectDoesNotHoldSameName(t *testing.T) {
	for _, tc := range []struct {
		err  error
		held bool
	}{
		{errors.New("PUT: 413: too large"), false},
		{httpErr(422), false},
		{errors.New("PUT: 409: conflict"), true},
		{httpErr(429), true},
		{errors.New("PUT: 507: quota"), true},
	} {
		e, root := indexRows(t, []row{
			{n: index.Node{NodeID: "A", RelPath: "Archive", LocalPath: "Archive", IsDir: true, Version: 1}, seq: 1},
			{n: index.Node{NodeID: "V", RelPath: "Archive/video.mp4", LocalPath: "Archive/video.mp4", Version: 1}, seq: 2, content: "old video"},
			{n: index.Node{NodeID: "I", RelPath: "Inbox", LocalPath: "Inbox", IsDir: true, Version: 1}, seq: 3},
		})
		if err := os.RemoveAll(filepath.Join(root, "Archive")); err != nil {
			t.Fatal(err)
		}
		writeAt(t, filepath.Join(root, "Inbox", "video.mp4"), "huge new video")
		var logged strings.Builder
		log.SetOutput(&logged)
		s := failingSink{newFakeSink(), nil, map[string]error{"Inbox/video.mp4": tc.err}}
		_ = e.PushLocal(context.Background(), s)
		log.SetOutput(os.Stderr)
		if held := !deletedOn(s.fakeSink, "Archive"); held != tc.held {
			t.Errorf("%v: Archive held = %v, want %v (ops %v)", tc.err, held, tc.held, s.ops)
		}
		if tc.held && !strings.Contains(logged.String(), "Archive") {
			t.Errorf("%v: the held delete was not logged: %q", tc.err, logged.String())
		}
	}
}

type httpErr int

func (e httpErr) Error() string   { return "PUT /sync/file: " + strconv.Itoa(int(e)) }
func (e httpErr) HTTPStatus() int { return int(e) }

// Round 13: a chunked upload's session errors are about that session, not the file: the
// next pass starts a new one. They never count as a permanent rejection.
type statusErr struct {
	op   string
	code int
	body string
}

func (e statusErr) Error() string   { return e.op + ": " + strconv.Itoa(e.code) + ": " + e.body }
func (e statusErr) HTTPStatus() int { return e.code }

func TestChunkedSessionErrorsAreNotPermanent(t *testing.T) {
	for _, tc := range []struct {
		err       error
		permanent bool
	}{
		{statusErr{"/upload chunk 3", 404, `{"error":"upload session not found"}`}, false},
		{statusErr{"/upload chunk 3", 400, `{"error":"chunk exceeds the declared file size"}`}, false},
		{statusErr{"/upload chunk 0", 400, `{"error":"invalid chunk number"}`}, false},
		{statusErr{"/upload complete", 400, `{"error":"upload incomplete"}`}, false},
		{statusErr{"/upload complete", 404, `{"error":"upload session not found"}`}, false},
		{statusErr{"/upload status", 404, `{"error":"upload session not found"}`}, false},
		{fmt.Errorf("wrapped: %w", statusErr{"/upload chunk 1", 404, `{"error":"upload session not found"}`}), false},
		{statusErr{"/upload/init", 413, `{"error":"file too large"}`}, true},
		{statusErr{"PUT /sync/file", 413, `{"error":"file too large"}`}, true},
		{errors.New("/upload chunk 2: 404: upload session not found"), false},
	} {
		if got := rejectedForGood(tc.err); got != tc.permanent {
			t.Errorf("%v: permanent = %v, want %v", tc.err, got, tc.permanent)
		}
	}
	// And in a pass: a lost session keeps the same-name hold.
	e, root := indexRows(t, []row{
		{n: index.Node{NodeID: "A", RelPath: "Archive", LocalPath: "Archive", IsDir: true, Version: 1}, seq: 1},
		{n: index.Node{NodeID: "V", RelPath: "Archive/video.mp4", LocalPath: "Archive/video.mp4", Version: 1}, seq: 2, content: "old video"},
		{n: index.Node{NodeID: "I", RelPath: "Inbox", LocalPath: "Inbox", IsDir: true, Version: 1}, seq: 3},
	})
	if err := os.RemoveAll(filepath.Join(root, "Archive")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, filepath.Join(root, "Inbox", "video.mp4"), "moved video, edited")
	s := failingSink{newFakeSink(), nil, map[string]error{"Inbox/video.mp4": statusErr{"/upload chunk 3", 404, `{"error":"upload session not found"}`}}}
	_ = e.PushLocal(context.Background(), s)
	if deletedOn(s.fakeSink, "Archive") || deletedOn(s.fakeSink, "Archive/video.mp4") {
		t.Errorf("a lost upload session let the source go (ops %v)", s.ops)
	}
}
