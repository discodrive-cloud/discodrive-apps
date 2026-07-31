package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRestMutations(t *testing.T) {
	hits := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	node := func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"id": "n", "name": "x", "is_dir": false, "version": 1})
	}
	mux.HandleFunc("POST /files/folder", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		hits["folder"] = b["name"].(string)
		node(w)
	})
	mux.HandleFunc("POST /files/upload", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		hits["upload"] = r.FormValue("name")
		node(w)
	})
	mux.HandleFunc("PATCH /files/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		hits["rename"] = r.PathValue("id") + ":" + b["name"].(string)
		node(w)
	})
	mux.HandleFunc("PATCH /files/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		hits["move"] = r.PathValue("id")
		node(w)
	})
	mux.HandleFunc("DELETE /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		hits["delete"] = r.PathValue("id")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "kfd")
	ctx := context.Background()

	if err := c.CreateFolder(ctx, "", "docs"); err != nil || hits["folder"] != "docs" {
		t.Fatalf("folder: %v %v", err, hits)
	}
	if err := c.UploadFile(ctx, "p1", "a.txt", strings.NewReader("hi"), time.Time{}); err != nil || hits["upload"] != "a.txt" {
		t.Fatalf("upload: %v %v", err, hits)
	}
	if err := c.RenameNode(ctx, "n9", "b.txt"); err != nil || hits["rename"] != "n9:b.txt" {
		t.Fatalf("rename: %v %v", err, hits)
	}
	if err := c.MoveNode(ctx, "n9", "p2"); err != nil || hits["move"] != "n9" {
		t.Fatalf("move: %v %v", err, hits)
	}
	if err := c.DeleteNode(ctx, "n9"); err != nil || hits["delete"] != "n9" {
		t.Fatalf("delete: %v %v", err, hits)
	}
}
