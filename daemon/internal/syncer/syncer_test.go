package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

func TestSyncOncePushThenPull(t *testing.T) {
	var mu sync.Mutex
	var order []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 0})
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "push")
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n", "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "pull")
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	// The order under test is that of an established mirror; a never-pulled index does
	// not push at all (see engine.establishMirror).
	idx.SetMirrorReady(true)
	client := protocol.New(srv.URL, "kfd")
	eng := engine.New(client, idx, root)
	s := New(client, eng, root, filepath.Join(t.TempDir(), "status.json"))

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[0] != "push" || order[len(order)-1] != "pull" {
		t.Fatalf("expected push before pull, got %v", order)
	}
}

// When the server's scope epoch differs from the client's, SyncOnce reconciles (fresh pull +
// orphan sweep) and does NOT push — local files were mapped to the old scope.
func TestSyncOnceReconcilesOnEpochChange(t *testing.T) {
	var mu sync.Mutex
	var pushed bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 1}) // index starts at 0 → mismatch
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pushed = true
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n", "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	// A local file from the "old scope" that must be swept (it's not in the new feed).
	if err := os.WriteFile(filepath.Join(root, "orphan.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	// The order under test is that of an established mirror; a never-pulled index does
	// not push at all (see engine.establishMirror).
	idx.SetMirrorReady(true)
	client := protocol.New(srv.URL, "kfd")
	eng := engine.New(client, idx, root)
	s := New(client, eng, root, filepath.Join(t.TempDir(), "status.json"))

	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if pushed {
		t.Fatal("reconcile pass must NOT push local files into the new scope")
	}
	if _, err := os.Stat(filepath.Join(root, "orphan.txt")); !os.IsNotExist(err) {
		t.Fatalf("orphan.txt should be swept on reconcile, err=%v", err)
	}
	if ep, _ := eng.ScopeEpoch(); ep != 1 {
		t.Fatalf("ScopeEpoch=%d, want 1 after reconcile", ep)
	}
}
