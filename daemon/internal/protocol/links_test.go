package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSaveLinkReportsStatus(t *testing.T) {
	var got map[string]string
	status := http.StatusOK
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "J"})
	})
	mux.HandleFunc("POST /me/links", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer J" {
			t.Error("unauthenticated save")
		}
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewUnscoped(srv.URL, "device")
	if code, err := c.SaveLink(context.Background(), "https://www.instagram.com/reel/A/", "Рецепт", time.Time{}); err != nil || code != 200 {
		t.Fatalf("save: %d %v", code, err)
	}
	if got["url"] != "https://www.instagram.com/reel/A/" || got["title"] != "Рецепт" {
		t.Fatalf("sent %v", got)
	}
	if _, has := got["shared_at"]; has {
		t.Fatal("a fresh share sends no shared_at")
	}
	queued := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	if _, err := c.SaveLink(context.Background(), "https://x/q", "", queued); err != nil || got["shared_at"] != "2026-11-07T10:00:00Z" {
		t.Fatalf("queued share: %v %v", got, err)
	}
	status = http.StatusBadRequest
	if code, err := c.SaveLink(context.Background(), "https://x/", "", time.Time{}); err != nil || code != 400 {
		t.Fatalf("rejected: %d %v", code, err)
	}
	if _, has := got["title"]; has {
		t.Fatal("an empty title must not be sent")
	}
}

// A device the server no longer knows answers 401, not an error: the app shows "sign in".
func TestSaveLinkUnpairedDevice(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if code, err := NewUnscoped(srv.URL, "gone").SaveLink(context.Background(), "https://x/", "", time.Time{}); err != nil || code != 401 {
		t.Fatalf("got %d %v", code, err)
	}
}
