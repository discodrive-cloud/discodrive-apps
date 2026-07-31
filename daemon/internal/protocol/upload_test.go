package protocol

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func tokenMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	return mux
}

func TestChunkedUpload(t *testing.T) {
	var (
		gotName, gotParent string
		gotChunks          = map[int]string{}
		completed, aborted bool
	)
	mux := tokenMux()
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotName, _ = b["name"].(string)
		gotParent, _ = b["parent_id"].(string)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "u1", "next_chunk": 0})
	})
	mux.HandleFunc("PUT /upload/{id}/chunk/{n}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n, _ := strconv.Atoi(r.PathValue("n"))
		gotChunks[n] = string(body)
		_ = json.NewEncoder(w).Encode(map[string]any{"next_chunk": n + 1})
	})
	mux.HandleFunc("GET /upload/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"next_chunk": 2})
	})
	mux.HandleFunc("POST /upload/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		completed = true
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"conflicted": false, "node": map[string]any{"id": "n1"}})
	})
	mux.HandleFunc("DELETE /upload/{id}", func(w http.ResponseWriter, r *http.Request) {
		aborted = true
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "dt")
	ctx := context.Background()

	id, next, err := c.UploadInit(ctx, "p1", "a.bin", 3, time.Time{})
	if err != nil || id != "u1" || next != 0 {
		t.Fatalf("init: id=%q next=%d err=%v", id, next, err)
	}
	if gotName != "a.bin" || gotParent != "p1" {
		t.Fatalf("init recorded name=%q parent=%q", gotName, gotParent)
	}

	n, err := c.UploadChunk(ctx, "u1", 0, strings.NewReader("abc"), nil)
	if err != nil || n != 1 || gotChunks[0] != "abc" {
		t.Fatalf("chunk: n=%d err=%v chunk0=%q", n, err, gotChunks[0])
	}

	st, err := c.UploadStatus(ctx, "u1")
	if err != nil || st != 2 {
		t.Fatalf("status: %d err=%v", st, err)
	}

	if err := c.UploadComplete(ctx, "u1"); err != nil || !completed {
		t.Fatalf("complete: err=%v completed=%v", err, completed)
	}
	if err := c.UploadAbort(ctx, "u1"); err != nil || !aborted {
		t.Fatalf("abort: err=%v aborted=%v", err, aborted)
	}
}
