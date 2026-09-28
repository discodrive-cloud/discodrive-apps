package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func jwtWith(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"HS256"}`)) + "." + enc.EncodeToString(b) + ".sig"
}

// Signing out must end the device on the server too: a token left valid there keeps
// working for whoever holds a copy.
func TestRevokeDeviceDeletesItself(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": jwtWith(map[string]any{"sub": "u", "did": "5ae54550-a0eb-4081-ac36-0d7761ba2fe3"})})
	})
	mux.HandleFunc("DELETE /devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("unauthenticated delete")
		}
		deleted = r.PathValue("id")
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := NewUnscoped(srv.URL, "device").RevokeDevice(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deleted != "5ae54550-a0eb-4081-ac36-0d7761ba2fe3" {
		t.Fatalf("deleted %q", deleted)
	}
}

// Already revoked (or never exchanged): nothing to do, not an error.
func TestRevokeDeviceAlreadyGone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := NewUnscoped(srv.URL, "device").RevokeDevice(context.Background()); err != nil {
		t.Fatalf("revoked device must not be an error: %v", err)
	}
}

// A token without a device id (a password session) is never used to delete anything.
func TestRevokeDeviceNeedsDeviceID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": jwtWith(map[string]any{"sub": "u"})})
	})
	mux.HandleFunc("DELETE /devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		t.Error("delete without a device id")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := NewUnscoped(srv.URL, "device").RevokeDevice(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}
