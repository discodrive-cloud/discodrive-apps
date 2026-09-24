package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppleProfileKeepsPairedOrigin(t *testing.T) {
	returnedPath := "/apple-profile/token/DiscoDrive.mobileconfig"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "fixture"})
	})
	mux.HandleFunc("POST /me/apple-profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing authentication")
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"download_path": returnedPath})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewUnscoped(server.URL, "device")
	result, err := client.AppleProfile(context.Background(), "installation", true, true)
	if err != nil || result != server.URL+returnedPath {
		t.Fatalf("result %q, %v", result, err)
	}
	returnedPath = "https://untrusted.test/profile"
	if _, err := client.AppleProfile(context.Background(), "installation", true, true); err == nil {
		t.Fatal("external profile URL accepted")
	}
}
