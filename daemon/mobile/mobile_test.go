package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
