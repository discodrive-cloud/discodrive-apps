package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The upload paths answer differently depending on what went wrong — a stale session (404),
// a gap in the chunk sequence (409), a size mismatch (400) — and the callers have to react
// differently to each. A bare "unexpected status" string forces string matching, so these
// errors carry the code.
func TestUploadErrorsCarryTheStatusCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"upload_id": "u1", "next_chunk": 0})
	})
	mux.HandleFunc("PUT /upload/{id}/chunk/{n}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "gone":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "upload session not found"})
		default:
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "chunk out of order", "next_chunk": 2})
		}
	})
	mux.HandleFunc("POST /upload/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "upload is incomplete: staged bytes do not match the declared size"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "kfd")
	ctx := context.Background()

	if _, _, err := c.UploadInit(ctx, "", "a.jpg", 10, time.Time{}); err != nil {
		t.Fatalf("init: %v", err)
	}

	var se *StatusError
	_, err := c.UploadChunk(ctx, "u1", 0, strings.NewReader("x"), nil)
	if !errors.As(err, &se) {
		t.Fatalf("chunk error is %T (%v), want *StatusError", err, err)
	}
	if se.Code != http.StatusConflict {
		t.Fatalf("chunk out of order: code %d, want 409", se.Code)
	}
	// The body carries next_chunk; a caller resyncing from it must be able to read it.
	if !strings.Contains(se.Body, "next_chunk") {
		t.Fatalf("chunk error body = %q, want the server's payload", se.Body)
	}

	se = nil
	_, err = c.UploadChunk(ctx, "gone", 0, strings.NewReader("x"), nil)
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("stale session: %v, want a 404 StatusError", err)
	}

	se = nil
	err = c.UploadComplete(ctx, "u1")
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest {
		t.Fatalf("complete: %v, want a 400 StatusError", err)
	}
	if !strings.Contains(err.Error(), "staged bytes") {
		t.Fatalf("complete error = %q, want the server's explanation in the message", err)
	}
}
