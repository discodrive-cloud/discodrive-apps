package desktop

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

// fakeServer is an in-memory ServerAPI double. Tests set the fields they need and
// inspect the recorded calls.
type fakeServer struct {
	// Changes: returns pages[callIdx]; each page is (changes, nextCursor, hasMore).
	pages     []fakePage
	pageIdx   int
	download  map[string][]byte // nodeID -> content served by Download
	downloads int               // count of Download calls (re-download detection)

	// recorded mutation calls
	created  [][2]string // {parentID, name}
	renamed  [][2]string // {nodeID, newName}
	moved    [][2]string // {nodeID, newParentID}
	deleted  []string    // nodeID
	uploaded []string    // name

	gone     map[string]bool // node ids the server answers 404 not-found for
	failWith error           // when set, every node mutation fails with it
}

// notFound is the server's answer about a node it does not have.
var notFound = &protocol.StatusError{Op: "fake", Code: 404, Body: `{"error":"not found"}`}

func (f *fakeServer) NodeExists(_ context.Context, nodeID string) (bool, error) {
	return !f.gone[nodeID], nil
}

type fakePage struct {
	changes []engine.Change
	next    int64
	hasMore bool
}

func (f *fakeServer) Changes(_ context.Context, _ int64, _ int) ([]engine.Change, int64, bool, error) {
	if f.pageIdx >= len(f.pages) {
		return nil, 0, false, nil
	}
	p := f.pages[f.pageIdx]
	f.pageIdx++
	return p.changes, p.next, p.hasMore, nil
}

func (f *fakeServer) Download(_ context.Context, nodeID string, w io.Writer) error {
	f.downloads++
	if f.gone[nodeID] {
		return notFound
	}
	_, err := w.Write(f.download[nodeID])
	return err
}

func (f *fakeServer) CreateFolder(_ context.Context, parentID, name string) error {
	f.created = append(f.created, [2]string{parentID, name})
	return nil
}
func (f *fakeServer) RenameNode(_ context.Context, nodeID, newName string) error {
	if err := f.fail(nodeID); err != nil {
		return err
	}
	f.renamed = append(f.renamed, [2]string{nodeID, newName})
	return nil
}
func (f *fakeServer) MoveNode(_ context.Context, nodeID, newParentID string) error {
	if err := f.fail(nodeID); err != nil {
		return err
	}
	if f.gone[newParentID] {
		return notFound
	}
	f.moved = append(f.moved, [2]string{nodeID, newParentID})
	return nil
}
func (f *fakeServer) DeleteNode(_ context.Context, nodeID string) error {
	if err := f.fail(nodeID); err != nil {
		return err
	}
	f.deleted = append(f.deleted, nodeID)
	return nil
}
func (f *fakeServer) fail(nodeID string) error {
	if f.failWith != nil {
		return f.failWith
	}
	if f.gone[nodeID] {
		return notFound
	}
	return nil
}

func (f *fakeServer) UploadFile(_ context.Context, parentID, name string, r io.Reader, _ time.Time) error {
	_, _ = io.Copy(io.Discard, r)
	f.uploaded = append(f.uploaded, name)
	return nil
}

func (f *fakeServer) EnsureDir(_ context.Context, relPath string) (engine.RemoteNode, error) {
	return engine.RemoteNode{NodeID: "dir-" + relPath, Version: 1}, nil
}

func (f *fakeServer) PushFile(_ context.Context, relPath string, _ *int64, r io.Reader, _ time.Time) (engine.RemoteNode, bool, error) {
	_, _ = io.Copy(io.Discard, r)
	return engine.RemoteNode{NodeID: "file-" + relPath, Version: 1}, false, nil
}

// idxHandle wraps a temp index so tests can reach it and close it.
type idxHandle struct{ idx *index.Index }

// testingT is the slice of *testing.T the test helpers need.
type testingT interface {
	TempDir() string
	Fatalf(string, ...any)
	Cleanup(func())
}

func openTempIndex(t testingT) *idxHandle {
	p := filepath.Join(t.TempDir(), "index.db")
	idx, err := index.Open(p)
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return &idxHandle{idx: idx}
}

// newTestController builds a controller backed by a temp index and content dir.
func newTestController(t testingT, f ServerAPI) (*Controller, *idxHandle) {
	h := openTempIndex(t)
	return NewController(f, h.idx, t.TempDir()), h
}
