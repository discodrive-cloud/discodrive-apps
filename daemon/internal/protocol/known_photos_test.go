package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// KnownPhotoHashes sends the hashes and reads back the known ones; a server without the
// gallery route (404) or without media tools (409) knows none, and other failures surface.
func TestKnownPhotoHashes(t *testing.T) {
	status := http.StatusOK
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-123"})
	})
	mux.HandleFunc("POST /me/photos/v1/known", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Hashes []string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked = req.Hashes
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"known": []string{"AA"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "kfd_test")
	ctx := context.Background()

	known, err := c.KnownPhotoHashes(ctx, []string{"aa", "bb"})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 || !known["aa"] || known["bb"] {
		t.Fatalf("asked %v, known %v", asked, known)
	}
	for _, code := range []int{http.StatusNotFound, http.StatusConflict} {
		status = code
		if known, err := c.KnownPhotoHashes(ctx, []string{"aa"}); err != nil || len(known) != 0 {
			t.Fatalf("%d: known %v, err %v", code, known, err)
		}
	}
	status = http.StatusInternalServerError
	if _, err := c.KnownPhotoHashes(ctx, []string{"aa"}); err == nil {
		t.Fatal("a 500 must be an error")
	}
}
