package fullsync

import (
	"context"
	"crypto/sha256"
	"discodrive.org/daemon/internal/config"
	"encoding/hex"
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
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncPersistsRestartsAndDetaches(t *testing.T) {
	var uploads atomic.Int32
	content := []byte("remote")
	hash := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/device/token" && r.Header.Get("X-Discodrive-Scope") != "1" {
			t.Error("unscoped mirror request")
		}
		switch r.URL.Path {
		case "/auth/device/token":
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
		case "/sync/meta":
			json.NewEncoder(w).Encode(map[string]int{"scope_epoch": 4})
		case "/sync/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/sync/changes":
			changes := []any{}
			if r.URL.Query().Get("since") == "0" {
				changes = append(changes, map[string]any{"seq": 1, "op": "create", "node_id": "r", "path": "note.md", "version": 1, "content_hash": hex.EncodeToString(hash[:]), "size": len(content)})
			}
			json.NewEncoder(w).Encode(map[string]any{"changes": changes, "cursor": 1, "has_more": false})
		case "/files/r/content":
			w.Write(content)
		case "/sync/file":
			uploads.Add(1)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "local", "version": 1}, "conflicted": false})
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	}))
	defer server.Close()
	base := t.TempDir()
	profile := filepath.Join(base, "profile")
	root := filepath.Join(base, "mirror")
	write(t, filepath.Join(root, ".obsidian", "config.json"), "old settings")
	before, _ := identity(root)
	cfg := config.Config{ServerURL: server.URL + "/", DeviceToken: "device"}
	m := New(profile)
	if err := m.Attach(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), true); err == nil {
		t.Fatal("enabled without selected folder")
	}
	if err := m.Choose(root); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	wait(t, func() bool { return m.Status().State == "idle" })
	if got := read(t, filepath.Join(root, "note.md")); got != "remote" {
		t.Fatal(got)
	}
	if got := read(t, filepath.Join(m.Status().Backup, ".obsidian", "config.json")); got != "old settings" {
		t.Fatal(got)
	}
	after, _ := identity(root)
	if before != after {
		t.Fatal("replaced root")
	}
	if err := m.Choose(t.TempDir()); err == nil {
		t.Fatal("changed running mirror")
	}
	write(t, filepath.Join(root, "local.md"), "local edit")
	wait(t, func() bool { return uploads.Load() > 0 })
	m.Close()
	restarted := New(profile)
	defer restarted.Close()
	if !restarted.Status().Enabled || restarted.Status().Folder == "" {
		t.Fatal("preferences lost")
	}
	if err := restarted.Attach(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return restarted.Status().State == "idle" })
	if read(t, filepath.Join(root, "local.md")) != "local edit" {
		t.Fatal("restart backed up mirror again")
	}
	if err := restarted.Detach(); err != nil {
		t.Fatal(err)
	}
	if restarted.Status().Enabled || New(profile).Status().Enabled {
		t.Fatal("logout retained enable flag")
	}
	if err := restarted.Enable(context.Background(), true); err == nil {
		t.Fatal("unpaired client resumed old account")
	}
	if err := restarted.Attach(context.Background(), config.Config{ServerURL: server.URL, DeviceToken: "other"}); err != nil {
		t.Fatal(err)
	}
	if restarted.Status().State != "stopped" {
		t.Fatal("pairing silently started mirror")
	}
}

func TestCancelDrainsRequests(t *testing.T) {
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
	m := New(t.TempDir())
	defer m.Close()
	if err := m.Attach(context.Background(), config.Config{ServerURL: server.URL, DeviceToken: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Choose(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no request")
	}
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("close left requests alive")
	}
	if err := m.Enable(context.Background(), true); err == nil {
		t.Fatal("restarted after shutdown")
	}
}

func TestRejectsCacheAncestorsAndVaultPlaintext(t *testing.T) {
	base := t.TempDir()
	profile := filepath.Join(base, "profile")
	cache := filepath.Join(profile, "content")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	vault := filepath.Join(base, "ddvault-open", "docs")
	if err := os.MkdirAll(vault, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base, profile, cache, vault} {
		if _, err := validateRoot(path, profile); err == nil {
			t.Errorf("allowed %s", path)
		}
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(cache, alias); err == nil {
		if _, err := validateRoot(alias, profile); err == nil {
			t.Fatal("allowed symlink to cache")
		}
	}
}

func TestPreparationFailureKeepsOriginalAndNoReadyMarker(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	write(t, filepath.Join(root, "keep.md"), "keep")
	// Force failure before any move, irrespective of the test user's permissions.
	if err := os.Mkdir(filepath.Join(state, "last-backup.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepare(root, state); err == nil {
		t.Fatal("expected backup failure")
	}
	if read(t, filepath.Join(root, "keep.md")) != "keep" {
		t.Fatal("original lost")
	}
	if _, err := os.Stat(filepath.Join(state, "prepared.json")); !os.IsNotExist(err) {
		t.Fatal("marked failed preparation ready")
	}
}

func TestReplacementDirectoryCannotInheritDeletions(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "mirror")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := identity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(base, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkIdentity(root, id); err == nil {
		t.Fatal("accepted replacement root")
	}
}
func TestCorruptPreferencesNeverAutoEnable(t *testing.T) {
	profile := t.TempDir()
	write(t, filepath.Join(profile, "full-sync.json"), `{"folder":"/somewhere","enabled":true,"broken":`)
	if New(profile).Status().Enabled {
		t.Fatal("partially parsed preferences enabled sync")
	}
}

func TestDeletionRequiresConfirmation(t *testing.T) {
	var deleted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/device/token":
			json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
		case r.URL.Path == "/sync/meta":
			json.NewEncoder(w).Encode(map[string]int{"scope_epoch": 0})
		case r.URL.Path == "/sync/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case r.URL.Path == "/sync/changes":
			changes := []any{}
			if r.URL.Query().Get("since") == "0" {
				for i := 1; i <= 10; i++ {
					name := fmt.Sprintf("%d.md", i)
					changes = append(changes, map[string]any{"seq": i, "op": "create", "node_id": name, "path": name, "version": 1, "size": 1, "content_hash": fmt.Sprintf("%x", sha256.Sum256([]byte("x")))})
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"changes": changes, "cursor": 10, "has_more": false})
		case strings.HasPrefix(r.URL.Path, "/files/"):
			w.Write([]byte("x"))
		case r.Method == http.MethodDelete && r.URL.Path == "/sync/file":
			deleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	m := New(t.TempDir())
	defer m.Close()
	root := t.TempDir()
	if err := m.Attach(context.Background(), config.Config{ServerURL: srv.URL, DeviceToken: "device"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Choose(root); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	wait(t, func() bool { return m.Status().State == "idle" })
	for i := 1; i <= 10; i++ {
		if err := os.Remove(filepath.Join(root, fmt.Sprintf("%d.md", i))); err != nil {
			t.Fatal(err)
		}
	}
	wait(t, func() bool { return m.Status().ErrorKind == "bulk_delete" })
	if deleted.Load() != 0 {
		t.Fatal("unconfirmed deletion")
	}
	m.ConfirmDeletion()
	wait(t, func() bool { return deleted.Load() == 10 })
}
