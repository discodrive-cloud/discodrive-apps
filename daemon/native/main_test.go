package main

import (
	"crypto/sha256"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/syncer"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func wait(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
func idle() bool {
	native.Lock()
	defer native.Unlock()
	return native.status.State == syncer.StateIdle
}
func TestEmbeddedMirrorPullsPushesAndRestarts(t *testing.T) {
	content := []byte("server note")
	hash := sha256.Sum256(content)
	var uploads atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Discodrive-Scope") != "1" {
			t.Error("missing scope opt-in")
		}
		json.NewEncoder(w).Encode(map[string]int{"scope_epoch": 7})
	})
	mux.HandleFunc("GET /sync/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		changes := []any{}
		if r.URL.Query().Get("since") == "0" {
			changes = append(changes, map[string]any{"seq": 1, "op": "upsert", "node_id": "remote", "path": "remote.md", "version": 1, "size": len(content), "content_hash": hex.EncodeToString(hash[:])})
		}
		json.NewEncoder(w).Encode(map[string]any{"changes": changes, "cursor": 1, "has_more": false})
	})
	mux.HandleFunc("GET /files/remote/content", func(w http.ResponseWriter, r *http.Request) { w.Write(content) })
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Discodrive-Scope") != "1" {
			t.Error("unscoped upload")
		}
		uploads.Add(1)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "local", "version": 1}, "conflicted": false})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	root := t.TempDir()
	before, _ := os.Stat(root)
	cfg := configuration{server.URL + "/", "device", root, filepath.Join(t.TempDir(), "state.db"), ""}
	if err := start(cfg); err != nil {
		t.Fatal(err)
	}
	defer stop()
	wait(t, idle)
	got, err := os.ReadFile(filepath.Join(root, "remote.md"))
	if err != nil || string(got) != string(content) {
		t.Fatalf("download = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(root, "local.md"), []byte("local note"), 0600); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return uploads.Load() > 0 })
	stop()
	after, _ := os.Stat(root)
	if !os.SameFile(before, after) {
		t.Fatal("selected root replaced")
	}
	if err := start(cfg); err != nil {
		t.Fatal(err)
	}
	wait(t, idle)
	if _, err := os.Stat(filepath.Join(root, "local.md")); err != nil {
		t.Fatal("restart lost local file", err)
	}
}
func TestStopCancelsOutstandingRequest(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/token" {
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	if err := start(configuration{server.URL, "device", t.TempDir(), filepath.Join(t.TempDir(), "state.db"), ""}); err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request not started")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop left network work running")
	}
}

func TestMassDeletionWaitsForHostConfirmation(t *testing.T) {
	var deletions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/device/token":
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
		case "/sync/meta":
			json.NewEncoder(w).Encode(map[string]int{"scope_epoch": 0})
		case "/sync/changes":
			json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
		case "/sync/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/sync/file":
			if r.Method == http.MethodDelete {
				deletions.Add(1)
				w.WriteHeader(http.StatusNoContent)
			} else {
				t.Error("unexpected write")
			}
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	database := filepath.Join(t.TempDir(), "state.db")
	idx, err := index.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.SetMirrorReady(true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("%d.md", i)
		if err := idx.Put(index.Node{NodeID: name, RelPath: name, LocalPath: name, Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	idx.Close()
	if err := start(configuration{server.URL, "device", t.TempDir(), database, ""}); err != nil {
		t.Fatal(err)
	}
	defer stop()
	wait(t, func() bool { native.Lock(); defer native.Unlock(); return native.status.ErrorKind == "bulk_delete" })
	if deletions.Load() != 0 {
		t.Fatal("deleted without confirmation")
	}
	DDFullSyncConfirmDeletion()
	wait(t, func() bool { return deletions.Load() == 10 })
}

func TestRootReplacementStopsPass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Sync")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRoot(root, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+".old"); err != nil {
		t.Fatal(err)
	}
	if err := checkRoot(root, original); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkRoot(root, original); err == nil {
		t.Fatal("replacement root accepted")
	}
}

// The app hands over the pin saved at pairing under "Pin"; with it the engine talks to a
// server whose self-signed certificate the user trusted.
func TestEmbeddedMirrorUsesThePin(t *testing.T) {
	reached := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/token" {
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
			return
		}
		select {
		case reached <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	var cfg configuration
	raw := fmt.Sprintf(`{"Server":%q,"Token":"device","Root":%q,"Database":%q,"Pin":%q}`,
		server.URL, t.TempDir(), filepath.Join(t.TempDir(), "state.db"), protocol.Fingerprint(server.Certificate().Raw))
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := start(cfg); err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("pinned engine never reached the server")
	}
}
