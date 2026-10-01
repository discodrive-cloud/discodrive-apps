package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A file the server rejects is that file's problem: the pass still pulls everyone else's
// changes (like syncer.SyncOnce), and the rejection is still reported.
func TestRejectedUploadDoesNotStopThePull(t *testing.T) {
	var remote atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 0})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		if !remote.Load() || r.URL.Query().Get("since") != "0" {
			json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
			return
		}
		_, _ = w.Write([]byte(`{"changes":[{"seq":1,"op":"create","node_id":"n1","path":"down.txt","is_dir":false,"version":1,"content_hash":"","size":6,"deleted":false}],"cursor":1,"has_more":false}`))
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
	})
	mux.HandleFunc("GET /files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("remote"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, dir := newClient(t, srv.URL)
	if err := c.SyncOnce(); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), []byte("rejected"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote.Store(true)
	if err := c.SyncOnce(); err == nil {
		t.Fatal("the rejected upload was not reported")
	}
	// The pass finished: the phone is not offline, the sync time moves on, and the
	// rejection is in the error field.
	st := c.Status()
	if st.State != "idle" {
		t.Fatalf("state after a rejected upload = %q, want idle", st.State)
	}
	if st.LastSyncUnix == 0 {
		t.Fatal("LastSyncUnix did not move on after a pass that finished")
	}
	if !strings.Contains(st.LastError, "big.bin") {
		t.Fatalf("LastError = %q, want the rejected file named", st.LastError)
	}
	got, err := os.ReadFile(filepath.Join(dir, "down.txt"))
	if err != nil || string(got) != "remote" {
		t.Fatalf("the pull did not run after a rejected upload: %q, %v", got, err)
	}
}
