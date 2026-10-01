package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The server only reports feed deletes of permanently purged nodes to a client that
// declares it applies deletes by node id (and ignores deletes for ids it does not have).
// Every request this client makes must carry that declaration, not just the changes
// request — a client that skips it on some call is one the server cannot tell apart from
// an old, path-deleting release on that call.
func TestFeaturesHeaderSentOnEveryRequest(t *testing.T) {
	seen := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		seen["/auth/device/token"] = r.Header.Get(FeaturesHeader)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-123"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		seen["/sync/changes"] = r.Header.Get(FeaturesHeader)
		_ = json.NewEncoder(w).Encode(map[string]any{"changes": []map[string]any{}, "cursor": 0, "has_more": false})
	})
	mux.HandleFunc("GET /files/n1", func(w http.ResponseWriter, r *http.Request) {
		seen["/files/n1"] = r.Header.Get(FeaturesHeader)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "n1"})
	})
	mux.HandleFunc("DELETE /files/n1", func(w http.ResponseWriter, r *http.Request) {
		seen["DELETE /files/n1"] = r.Header.Get(FeaturesHeader)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /sync/dir", func(w http.ResponseWriter, r *http.Request) {
		seen["/sync/dir"] = r.Header.Get(FeaturesHeader)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "d1", "version": 1}})
	})
	mux.HandleFunc("PATCH /files/n1/rename", func(w http.ResponseWriter, r *http.Request) {
		seen["/files/n1/rename"] = r.Header.Get(FeaturesHeader)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "kfd_test")
	ctx := context.Background()

	if _, _, _, err := c.Changes(ctx, 0, 500); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if _, err := c.NodeExists(ctx, "n1"); err != nil {
		t.Fatalf("NodeExists: %v", err)
	}
	if err := c.DeleteNode(ctx, "n1"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if _, err := c.EnsureDir(ctx, "a/b"); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := c.RenameNode(ctx, "n1", "new"); err != nil {
		t.Fatalf("RenameNode: %v", err)
	}

	for path, got := range seen {
		if got != FeaturesValue {
			t.Errorf("%s: X-Discodrive-Features = %q, want %q", path, got, FeaturesValue)
		}
	}
	if len(seen) != 6 {
		t.Fatalf("expected 6 requests observed, got %d: %+v", len(seen), seen)
	}
}
