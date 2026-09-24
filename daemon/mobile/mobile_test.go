package mobile

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPairRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair/init", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "ABCD-EFGH", "verification_uri": "/pair?code=ABCD-EFGH", "interval": 1,
		})
	})
	mux.HandleFunc("POST /pair/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "approved", "device_token": "kfd_xyz"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p, err := PairBegin(srv.URL, "iPhone", "ios", false)
	if err != nil {
		t.Fatalf("PairBegin: %v", err)
	}
	if p.UserCode != "ABCD-EFGH" || p.DeviceCode != "dc" || p.IntervalSeconds != 1 {
		t.Fatalf("unexpected pairing: %+v", p)
	}
	if !strings.HasPrefix(p.VerificationURL, srv.URL) || !strings.Contains(p.VerificationURL, "/pair?code=ABCD-EFGH") {
		t.Fatalf("VerificationURL must be absolute: %q", p.VerificationURL)
	}

	tok, err := PairAwait(srv.URL, p.DeviceCode, 1, false)
	if err != nil || tok != "kfd_xyz" {
		t.Fatalf("PairAwait: tok=%q err=%v", tok, err)
	}
}

// syncMux builds a mock server. changes is the JSON body returned by GET /sync/changes (since=0);
// fileBody is what GET /files/{id}/content returns. pushed records PUT /sync/file paths.
func syncMux(changes, fileBody string, pushed *[]string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"scope_epoch": 0})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		if changes == "" {
			json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
			return
		}
		_, _ = w.Write([]byte(changes))
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		*pushed = append(*pushed, r.URL.Query().Get("path"))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n", "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("GET /files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fileBody))
	})
	return httptest.NewServer(mux)
}

func newClient(t *testing.T, serverURL string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := New(serverURL, "kfd_dev", dir, filepath.Join(t.TempDir(), "state.db"), false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, dir
}

func TestSyncOncePush(t *testing.T) {
	var pushed []string
	srv := syncMux("", "", &pushed)
	defer srv.Close()
	c, dir := newClient(t, srv.URL)
	// Whatever the folder held before the first pass is the device's previous life, not
	// new work: after pairing the server is the truth, and it is set aside, not uploaded.
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(pushed) != 0 {
		t.Fatalf("the first pass uploaded %v; nothing from before the pairing may go up", pushed)
	}
	st := c.Status()
	if st.SetAside == "" {
		t.Fatalf("status does not say where the old folder went: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(st.SetAside, "old.txt")); err != nil {
		t.Fatalf("old.txt is not in the set-aside folder %s: %v", st.SetAside, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old.txt is still in the sync folder")
	}
	// Work done after the pairing is pushed as before.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	found := false
	for _, p := range pushed {
		if p == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected push of a.txt, got %v", pushed)
	}
	if st := c.Status(); st.State != "idle" || st.LastSyncUnix == 0 {
		t.Fatalf("status after push: %+v", st)
	}
}

func TestSyncOncePull(t *testing.T) {
	body := "remote-data"
	changes := `{"changes":[{"seq":1,"op":"create","node_id":"n1","path":"down.txt","is_dir":false,"version":1,"content_hash":"","size":11,"deleted":false}],"cursor":1,"has_more":false}`
	var pushed []string
	srv := syncMux(changes, body, &pushed)
	defer srv.Close()
	c, dir := newClient(t, srv.URL)
	if err := c.SyncOnce(); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "down.txt"))
	if err != nil || string(got) != body {
		t.Fatalf("pulled file: %q err=%v", got, err)
	}
}

func TestSyncOnceOffline(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, _ := newClient(t, srv.URL)
	if err := c.SyncOnce(); err == nil {
		t.Fatal("expected SyncOnce to error when the server is down")
	}
	if st := c.Status(); st.State != "offline" || st.LastError == "" {
		t.Fatalf("status after failure: %+v", st)
	}
}

// A killed unpair or a failed unlink can leave the previous pairing's database behind.
// The server URL is unchanged, but its newer bytes must win even over offline local edits.
func TestRePairWithRetainedIndexDoesNotUploadOldFiles(t *testing.T) {
	var version atomic.Int64
	version.Store(1)
	var uploads atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]int{"scope_epoch": 0})
	})
	body := func() string {
		if version.Load() == 1 {
			return "old server bytes"
		}
		return "new server bytes"
	}
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		hash := sha256.Sum256([]byte(body()))
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{map[string]any{
			"seq": version.Load(), "op": "upsert", "node_id": "n", "path": "note.md", "version": version.Load(),
			"content_hash": fmt.Sprintf("%x", hash), "size": len(body())}}, "cursor": version.Load(), "has_more": false})
	})
	mux.HandleFunc("GET /files/n/content", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body())) })
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		uploads.Add(1)
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n", "version": 3}, "conflicted": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	root, db := filepath.Join(t.TempDir(), "Sync"), filepath.Join(t.TempDir(), "state.db")
	old, err := New(srv.URL, "old-pairing", root, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.SyncOnce(); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	// An ordinary restart with the same pairing must retain the established mirror.
	restarted, err := New(srv.URL, "old-pairing", root, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncOnce(); err != nil {
		restarted.Close()
		t.Fatal(err)
	}
	if restarted.Status().SetAside != "" {
		restarted.Close()
		t.Fatal("ordinary restart backed up the mirror")
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.md"), []byte("stale offline edit"), 0600); err != nil {
		t.Fatal(err)
	}
	version.Store(2)
	current, err := New(srv.URL, "new-pairing", root, db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if err := current.SyncOnce(); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 0 {
		t.Fatalf("re-pair uploaded old files %d times", uploads.Load())
	}
	if got, err := os.ReadFile(filepath.Join(root, "note.md")); err != nil || string(got) != "new server bytes" {
		t.Fatalf("mirror = %q, %v", got, err)
	}
	aside := current.Status().SetAside
	if got, err := os.ReadFile(filepath.Join(aside, "note.md")); err != nil || string(got) != "stale offline edit" {
		t.Fatalf("backup = %q, %v", got, err)
	}
	if err := current.SyncOnce(); err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 0 {
		t.Fatal("backup was uploaded in a later pass")
	}
}

// Closing cancels a blocked request and drains the current pass before closing SQLite.
func TestCloseCancelsSyncAndRejectsLaterWork(t *testing.T) {
	entered := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, _ := newClient(t, srv.URL)
	done := make(chan error, 1)
	go func() { done <- c.SyncOnce() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel request")
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled pass succeeded")
	}
	if err := c.SyncOnce(); err == nil {
		t.Fatal("closed client accepted a pass")
	}
	if err := c.ResetLocalIndex(); err == nil {
		t.Fatal("closed client accepted an index reset")
	}
	if !json.Valid([]byte(c.ActivityJSON())) {
		t.Fatal("invalid activity snapshot")
	}
}
