package mobile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"discodrive.org/daemon/internal/vault"
)

func encID(rel string) string { return base64.RawURLEncoding.EncodeToString([]byte(rel)) }
func decID(id string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(id)
	return string(b), err
}

// startVaultServer starts an httptest server backed by serverDir as the "server storage".
// node_id == base64url(rel_path) so Download maps cleanly. DELETE leaves a tombstone so the
// change-feed reports the removal (the dir walk alone can't).
func startVaultServer(t *testing.T, serverDir string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	var seq int64
	tombs := map[string]bool{}

	type ch struct {
		Seq         int64  `json:"seq"`
		Op          string `json:"op"`
		NodeID      string `json:"node_id"`
		Path        string `json:"path"`
		IsDir       bool   `json:"is_dir"`
		Version     int64  `json:"version"`
		ContentHash string `json:"content_hash"`
		Size        int64  `json:"size"`
		Deleted     bool   `json:"deleted"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var changes []ch
		filepath.WalkDir(serverDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(serverDir, p)
			if rel == "." {
				return nil
			}
			rel = filepath.ToSlash(rel)
			seq++
			var size int64
			if !d.IsDir() {
				if fi, e := d.Info(); e == nil {
					size = fi.Size()
				}
			}
			changes = append(changes, ch{
				Seq: seq, Op: "create", NodeID: encID(rel), Path: rel,
				IsDir: d.IsDir(), Version: 1, Size: size,
			})
			return nil
		})
		for rel := range tombs {
			seq++
			changes = append(changes, ch{Seq: seq, Op: "delete", NodeID: encID(rel), Path: rel, Deleted: true})
		}
		json.NewEncoder(w).Encode(map[string]any{"changes": changes, "cursor": seq, "has_more": false})
	})
	mux.HandleFunc("GET /files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		rel, err := decID(r.PathValue("id"))
		if err != nil {
			http.Error(w, "bad id", 400)
			return
		}
		data, err := os.ReadFile(filepath.Join(serverDir, filepath.FromSlash(rel)))
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Write(data)
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("path")
		dst := filepath.Join(serverDir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(dst), 0o755)
		data, _ := io.ReadAll(r.Body)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		mu.Lock()
		delete(tombs, rel)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": encID(rel), "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("POST /sync/dir", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if err := os.MkdirAll(filepath.Join(serverDir, filepath.FromSlash(body.Path)), 0o755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": encID(body.Path), "version": 1}})
	})
	mux.HandleFunc("DELETE /sync/file", func(w http.ResponseWriter, r *http.Request) {
		rel := r.URL.Query().Get("path")
		os.RemoveAll(filepath.Join(serverDir, filepath.FromSlash(rel)))
		mu.Lock()
		tombs[rel] = true
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// copyTree recursively copies src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyTree: %v", err)
	}
}

func openTestVault(t *testing.T, password string) *Vault {
	t.Helper()
	serverDir := t.TempDir()
	copyTree(t, "../internal/vault/testdata/cmvault", filepath.Join(serverDir, "MyVault"))
	srv := startVaultServer(t, serverDir)
	mv, err := OpenVault(srv.URL, "kfd", "MyVault", password,
		filepath.Join(t.TempDir(), "i.db"), t.TempDir(), "")
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	t.Cleanup(func() { mv.Close() })
	return mv
}

func vaultList(t *testing.T, mv *Vault, dirID string) []vaultEntry {
	t.Helper()
	js, err := mv.List(dirID)
	if err != nil {
		t.Fatalf("List(%q): %v", dirID, err)
	}
	var es []vaultEntry
	if err := json.Unmarshal([]byte(js), &es); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	return es
}

func vaultWalkFind(t *testing.T, mv *Vault, dirID string, want []byte) bool {
	for _, e := range vaultList(t, mv, dirID) {
		if e.IsDir {
			if vaultWalkFind(t, mv, e.DirID, want) {
				return true
			}
			continue
		}
		p, err := mv.OpenFile(e.FileStoragePath, e.Name)
		if err != nil {
			t.Fatalf("OpenFile(%q): %v", e.FileStoragePath, err)
		}
		data, _ := os.ReadFile(p)
		if bytes.Contains(data, want) {
			return true
		}
	}
	return false
}

func TestVaultOpenListRead(t *testing.T) {
	mv := openTestVault(t, "password123")
	want := []byte("Привет, мой верный друг!")
	if !vaultWalkFind(t, mv, "", want) {
		t.Fatalf("did not find %q via the facade", want)
	}
}

func vaultListHas(t *testing.T, mv *Vault, dirID, name string) bool {
	for _, e := range vaultList(t, mv, dirID) {
		if e.Name == name {
			return true
		}
	}
	return false
}

func vaultReadByName(t *testing.T, mv *Vault, dirID, name string) []byte {
	for _, e := range vaultList(t, mv, dirID) {
		if e.Name == name && !e.IsDir {
			p, err := mv.OpenFile(e.FileStoragePath, e.Name)
			if err != nil {
				t.Fatalf("OpenFile(%q): %v", name, err)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %q: %v", p, err)
			}
			return data
		}
	}
	t.Fatalf("file %q not found in dirID=%q", name, dirID)
	return nil
}

func TestVaultWriteRoundTrip(t *testing.T) {
	mv := openTestVault(t, "password123")

	local := filepath.Join(t.TempDir(), "new.txt")
	plain := []byte("brand new content")
	if err := os.WriteFile(local, plain, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mv.WriteFile("", "new.txt", local); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !vaultListHas(t, mv, "", "new.txt") {
		t.Fatal("new.txt is not visible at root after WriteFile")
	}
	if got := vaultReadByName(t, mv, "", "new.txt"); !bytes.Equal(got, plain) {
		t.Fatalf("round-trip: got %q want %q", got, plain)
	}

	if _, err := mv.Mkdir("", "sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if !vaultListHas(t, mv, "", "sub") {
		t.Fatal("sub is not visible after Mkdir")
	}

	if err := mv.Remove("", "new.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if vaultListHas(t, mv, "", "new.txt") {
		t.Fatal("new.txt is still visible after Remove")
	}
}

func TestVaultWrongPassword(t *testing.T) {
	serverDir := t.TempDir()
	copyTree(t, "../internal/vault/testdata/cmvault", filepath.Join(serverDir, "MyVault"))
	srv := startVaultServer(t, serverDir)
	_, err := OpenVault(srv.URL, "kfd", "MyVault", "wrong",
		filepath.Join(t.TempDir(), "i.db"), t.TempDir(), "")
	if !errors.Is(err, vault.ErrWrongPassword) {
		t.Fatalf("expected ErrWrongPassword, got %v", err)
	}
}

func TestVaultCloseRemovesPlaintextAndRejectsReads(t *testing.T) {
	mv := openTestVault(t, "password123")
	if err := os.WriteFile(filepath.Join(mv.tmpDir, "preview.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mv.tmpDir); !os.IsNotExist(err) {
		t.Fatalf("plaintext remains: %v", err)
	}
	if _, err := mv.List(""); err == nil {
		t.Fatal("closed vault accepted read")
	}
	if err := mv.Close(); err != nil {
		t.Fatal(err)
	}
}
