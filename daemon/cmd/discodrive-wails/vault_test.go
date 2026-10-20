package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"discodrive.org/daemon/internal/desktop"
	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
)

// treeServer is an in-memory server tree for App.CreateVault: it records every write
// and feeds them back through Changes.
type treeServer struct {
	mu      sync.Mutex
	changes []engine.Change
	paths   map[string]bool
}

func (s *treeServer) add(rel string, dir bool) engine.RemoteNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paths == nil {
		s.paths = map[string]bool{}
	}
	s.paths[rel] = true
	seq := int64(len(s.changes) + 1)
	s.changes = append(s.changes, engine.Change{Seq: seq, Op: "upsert", NodeID: rel, RelPath: rel, IsDir: dir, Version: 1})
	return engine.RemoteNode{NodeID: rel, Version: 1}
}

func (s *treeServer) Changes(_ context.Context, since int64, _ int) ([]engine.Change, int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []engine.Change
	for _, c := range s.changes {
		if c.Seq > since {
			out = append(out, c)
		}
	}
	return out, int64(len(s.changes)), false, nil
}
func (s *treeServer) EnsureDir(_ context.Context, rel string) (engine.RemoteNode, error) {
	return s.add(rel, true), nil
}
func (s *treeServer) PushFile(_ context.Context, rel string, _ *int64, r io.Reader, _ time.Time) (engine.RemoteNode, bool, error) {
	_, _ = io.Copy(io.Discard, r)
	return s.add(rel, false), false, nil
}
func (s *treeServer) Download(context.Context, string, io.Writer) error  { return nil }
func (s *treeServer) CreateFolder(context.Context, string, string) error { return nil }
func (s *treeServer) RenameNode(context.Context, string, string) error   { return nil }
func (s *treeServer) MoveNode(context.Context, string, string) error     { return nil }
func (s *treeServer) DeleteNode(context.Context, string) error           { return nil }
func (s *treeServer) NodeExists(context.Context, string) (bool, error)   { return true, nil }
func (s *treeServer) UploadFile(context.Context, string, string, io.Reader, time.Time) error {
	return nil
}

func newVaultApp(t *testing.T, srv *treeServer) *App {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	ctrl := desktop.NewController(srv, idx, t.TempDir())
	return &App{ctx: context.Background(), ctrl: ctrl, idx: idx, ready: true}
}

func TestAppCreateVaultInChosenFolder(t *testing.T) {
	srv := &treeServer{}
	srv.add("Work", true)
	a := newVaultApp(t, srv)
	if _, err := a.CreateVault("Work", "Secret", "pw"); err != nil {
		t.Fatalf("CreateVault: %v", err)
	}
	if !srv.paths["Work/Secret/vault.cryptomator"] {
		t.Fatalf("vault not created in Work/Secret: %v", srv.paths)
	}
	for p := range srv.paths {
		if strings.HasPrefix(p, "Secret") {
			t.Fatalf("vault written to the root: %q", p)
		}
	}
	if vs := a.Vaults(); len(vs) != 1 || vs[0].RelPath != "Work/Secret" {
		t.Fatalf("Vaults() = %+v", vs)
	}
}

func TestAppCreateVaultRejectsExistingFolder(t *testing.T) {
	srv := &treeServer{}
	srv.add("Documents", true)
	srv.add("Documents/Car insurance.pdf", false)
	a := newVaultApp(t, srv)
	before := len(srv.changes)
	_, err := a.CreateVault("", "Documents", "pw")
	if !errors.Is(err, desktop.ErrVaultNameTaken) {
		t.Fatalf("CreateVault = %v, want ErrVaultNameTaken", err)
	}
	// Wails hands the frontend the error text; i18n.js keys off its tag.
	if !strings.HasPrefix(err.Error(), "vault_name_taken:") {
		t.Fatalf("error text %q lacks the vault_name_taken: tag", err)
	}
	if len(srv.changes) != before {
		t.Fatalf("rejected CreateVault wrote %d nodes", len(srv.changes)-before)
	}
}
