package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAppleEnrollmentHTTPSAndDownloadPath(t *testing.T) {
	path := "/apple-enrollment/" + strings.Repeat("a", 64) + "/DiscoDrive.mobileconfig"
	returned := path
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "fixture"})
	})
	mux.HandleFunc("GET /me/apple-enrollment", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"enabled": false})
	})
	mux.HandleFunc("POST /me/apple-enrollment", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("unsafe request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["password"] != "test-secret" || body["device_id"] != "dav-device" || body["calendars"] != true || body["contacts"] != false {
			t.Error("incorrect enrollment body")
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"download_path": returned})
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client := NewUnscoped(server.URL, "device")
	client.hc = server.Client()
	if enabled, err := client.AppleEnrollmentAvailable(context.Background()); err != nil || enabled {
		t.Fatalf("capability: %v %v", enabled, err)
	}
	cred := DAVCredential{ID: "dav-device", Password: "test-secret"}
	got, err := client.AppleEnrollment(context.Background(), "installation", true, false, cred)
	if err != nil || got != server.URL+path {
		t.Fatalf("result %q %v", got, err)
	}
	for _, bad := range []string{"https://evil.test/profile", "//evil.test/profile", path + "?password=test-secret", path + "#fragment", strings.Replace(path, "aaaa", "AAaa", 1), strings.Replace(path, "aaaa", "%2fA", 1), strings.Replace(path, "aaaa", "../a", 1)} {
		returned = bad
		if _, err := client.AppleEnrollment(context.Background(), "installation", true, false, cred); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	client.baseURL = strings.Replace(server.URL, "https:", "http:", 1)
	if _, err := client.AppleEnrollment(context.Background(), "installation", true, false, cred); err == nil {
		t.Fatal("accepted HTTP")
	}
}
