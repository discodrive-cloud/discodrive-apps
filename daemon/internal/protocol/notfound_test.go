package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"discodrive.org/daemon/internal/engine"
)

// The server answers a node it no longer has (purged, trashed, never existed) with
// 404 {"error":"not found"}. Other 404s — an expired upload session, a blob missing on
// disk, a proxy's page — are not about the node and must not make a client forget it.
func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"node not found", &StatusError{Op: "DELETE /files", Code: 404, Body: `{"error":"not found"}`}, true},
		{"wrapped", fmt.Errorf("rename: %w", &StatusError{Code: 404, Body: `{"error":"not found"}`}), true},
		{"upload session", &StatusError{Code: 404, Body: `{"error":"upload session not found"}`}, false},
		{"blob missing", &StatusError{Code: 404, Body: `{"error":"file not found"}`}, false},
		{"proxy page", &StatusError{Code: 404, Body: `<html>404 Not Found</html>`}, false},
		{"empty body", &StatusError{Code: 404}, false},
		{"other status", &StatusError{Code: 403, Body: `{"error":"not found"}`}, false},
		{"plain error", errors.New("not found"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsNotFound(tc.err); got != tc.want {
			t.Errorf("%s: IsNotFound = %v, want %v", tc.name, got, tc.want)
		}
		if got := errors.Is(tc.err, engine.ErrNodeNotFound); got != tc.want {
			t.Errorf("%s: errors.Is(ErrNodeNotFound) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Every node-addressed call reports the server's 404 as a not-found error, including a
// download — which used to flatten the status into a string.
func TestNodeCallsReportNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	gone := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
	mux.HandleFunc("GET /files/{id}/content", gone)
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "live" {
			json.NewEncoder(w).Encode(map[string]any{"id": "live"})
			return
		}
		gone(w, r)
	})
	mux.HandleFunc("PATCH /files/{id}/rename", gone)
	mux.HandleFunc("PATCH /files/{id}/move", gone)
	mux.HandleFunc("DELETE /files/{id}", gone)
	mux.HandleFunc("GET /files/{id}/versions", gone)
	mux.HandleFunc("GET /files/{id}/shares", gone)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "dev")
	ctx := context.Background()

	calls := map[string]error{
		"download": c.Download(ctx, "x", new(countingWriter)),
		"rename":   c.RenameNode(ctx, "x", "y"),
		"move":     c.MoveNode(ctx, "x", ""),
		"delete":   c.DeleteNode(ctx, "x"),
	}
	_, calls["versions"] = c.Versions(ctx, "x")
	_, calls["shares"] = c.Shares(ctx, "x")
	for name, err := range calls {
		if !IsNotFound(err) {
			t.Errorf("%s: err = %v, want a not-found error", name, err)
		}
	}

	if ok, err := c.NodeExists(ctx, "live"); err != nil || !ok {
		t.Errorf("NodeExists(live) = %v, %v; want true", ok, err)
	}
	if ok, err := c.NodeExists(ctx, "x"); err != nil || ok {
		t.Errorf("NodeExists(gone) = %v, %v; want false", ok, err)
	}
}

func TestStatusCode(t *testing.T) {
	if got := StatusCode(fmt.Errorf("purge: %w", &StatusError{Code: 409})); got != 409 {
		t.Errorf("wrapped 409: got %d", got)
	}
	if got := StatusCode(errors.New("offline")); got != 0 {
		t.Errorf("plain error: got %d, want 0", got)
	}
	if got := StatusCode(nil); got != 0 {
		t.Errorf("nil: got %d, want 0", got)
	}
}

// NodeExists reports any answer other than the server's own "not found" as an error that
// carries its HTTP status: the sync engine tells a refusal (4xx: settle without guessing)
// from a temporary failure (5xx, 429: retry) by it.
func TestNodeExistsErrorsCarryTheStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	codes := map[string]int{"forbidden": 403, "proxy404": 404, "busy": 429, "down": 503}
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		code := codes[r.PathValue("id")]
		w.WriteHeader(code)
		fmt.Fprint(w, "<html>blocked</html>")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "dev")
	for id, code := range codes {
		ok, err := c.NodeExists(context.Background(), id)
		var hs interface{ HTTPStatus() int }
		if ok || !errors.As(err, &hs) || hs.HTTPStatus() != code {
			t.Errorf("%s: NodeExists = %v, %v; want an error with status %d", id, ok, err, code)
		}
	}
}
