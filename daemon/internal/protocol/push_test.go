package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientPushFileSendsBaseVersion(t *testing.T) {
	var gotPath, gotBase, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		gotBase = r.Header.Get("X-Base-Version")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "srv1", "version": 7}, "conflicted": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "kfd")
	base := int64(5)
	rn, conflicted, err := c.PushFile(context.Background(), "a/b.txt", &base, strings.NewReader("hello"), time.Time{})
	if err != nil || conflicted {
		t.Fatalf("push: err=%v conflicted=%v", err, conflicted)
	}
	if gotPath != "a/b.txt" || gotBase != "5" || gotBody != "hello" {
		t.Fatalf("request: path=%q base=%q body=%q", gotPath, gotBase, gotBody)
	}
	if rn.NodeID != "srv1" || rn.Version != 7 {
		t.Fatalf("response: %+v", rn)
	}
}

func TestClientEnsureDirAndDelete(t *testing.T) {
	var mkdirPath, delPath string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("POST /sync/dir", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		mkdirPath = b.Path
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "d1", "version": 1}})
	})
	mux.HandleFunc("DELETE /sync/file", func(w http.ResponseWriter, r *http.Request) {
		delPath = r.URL.Query().Get("path")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "kfd")
	if _, err := c.EnsureDir(context.Background(), "newdir"); err != nil || mkdirPath != "newdir" {
		t.Fatalf("ensuredir: err=%v path=%q", err, mkdirPath)
	}
	if err := c.DeleteRemote(context.Background(), "x/y.txt"); err != nil || delPath != "x/y.txt" {
		t.Fatalf("delete: err=%v path=%q", err, delPath)
	}
}
