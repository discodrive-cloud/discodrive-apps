package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	// A file synced under the "old scope" that must be swept (it's not in the new feed),
	// and one written offline that was never synced: the user's only copy, which stays.
	if err := os.WriteFile(filepath.Join(root, "orphan.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "offline.txt"), []byte("new work"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	sum := sha256.Sum256([]byte("stale"))
	idx.Put(index.Node{NodeID: "o1", RelPath: "orphan.txt", Version: 1, ContentHash: hex.EncodeToString(sum[:]), Size: 5})
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
	if b, err := os.ReadFile(filepath.Join(root, "offline.txt")); err != nil || string(b) != "new work" {
		t.Fatalf("unsynced offline.txt must survive reconcile: %q, %v", b, err)
	}
	if ep, _ := eng.ScopeEpoch(); ep != 1 {
		t.Fatalf("ScopeEpoch=%d, want 1 after reconcile", ep)
	}
}

// A file the server rejects must not hold back the other files' push, nor the pull that
// brings everyone else's changes; the rejection is still reported.
func TestSyncOncePullsDespiteRejectedUpload(t *testing.T) {
	var mu sync.Mutex
	var pushed []string
	pulled := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 0})
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "a-big.bin" {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		mu.Lock()
		pushed = append(pushed, p)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n-" + p, "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pulled = true
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	for name, body := range map[string]string{"a-big.bin": "too big", "b.txt": "fine"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true)
	client := protocol.New(srv.URL, "kfd")
	eng := engine.New(client, idx, root)
	s := New(client, eng, root, filepath.Join(t.TempDir(), "status.json"))

	err := s.SyncOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "a-big.bin") {
		t.Fatalf("SyncOnce error = %v, want one naming a-big.bin", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pushed) != 1 || pushed[0] != "b.txt" {
		t.Fatalf("pushed = %v, want [b.txt]", pushed)
	}
	if !pulled {
		t.Fatal("a rejected upload stopped the pull")
	}
}

// A pass whose only trouble is a file the server rejected is a finished pass: the rest was
// pushed and pulled, so the client is not "offline", the sync time moves on, and the loop
// does not hammer the server on a backoff schedule — the rejection is reported instead.
func TestRunReportsRejectedUploadWithoutGoingOffline(t *testing.T) {
	var mu sync.Mutex
	passes := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		passes++
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 0})
	})
	mux.HandleFunc("GET /sync/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.bin"), []byte("too big"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, _ := index.Open(filepath.Join(t.TempDir(), "s.db"))
	defer idx.Close()
	idx.SetMirrorReady(true)
	client := protocol.New(srv.URL, "kfd")
	eng := engine.New(client, idx, root)
	s := New(client, eng, root, filepath.Join(t.TempDir(), "status.json"))
	var got []Status
	done := make(chan struct{}, 1)
	s.ObserveStatus(func(st Status) {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
		if st.State != StateSyncing {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { _ = s.Run(ctx); close(stopped) }()
	defer func() { cancel(); <-stopped }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no pass finished")
	}
	// A failed pass is retried after 1 s (+500 ms debounce); a finished one waits for the
	// next trigger. Give the retry time to show up.
	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	var final Status
	for _, st := range got {
		if st.State != StateSyncing {
			final = st
			break
		}
	}
	if final.State != StateIdle {
		t.Fatalf("state after a rejected upload = %q, want %q", final.State, StateIdle)
	}
	if final.LastSync.IsZero() {
		t.Fatal("LastSync did not move on after a pass that finished")
	}
	if !strings.Contains(final.LastError, "big.bin") {
		t.Fatalf("LastError = %q, want the rejected file named", final.LastError)
	}
	if passes != 1 {
		t.Fatalf("passes = %d, want 1 (a rejected upload must not trigger backoff retries)", passes)
	}
}
