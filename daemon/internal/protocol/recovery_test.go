package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryAndSharingContracts(t *testing.T) {
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/token" {
			w.Write([]byte(`{"token":"J"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer J" {
			t.Error("missing authentication")
		}
		if r.Header.Get(scopeHeader) != "" {
			t.Error("browser operations must not use folder sync scope")
		}
		key := r.Method + " " + r.URL.Path
		calls[key]++
		switch key {
		case "GET /files/trash":
			w.Write([]byte(`[{"id":"f","name":"folder","is_dir":true,"size":null,"deleted_at":null}]`))
		case "GET /files/f/versions":
			w.Write([]byte(`[{"version":42,"size":null,"is_conflict_loser":true}]`))
		case "POST /files/f/restore":
			var body map[string]int64
			json.NewDecoder(r.Body).Decode(&body)
			if body["version"] != 42 {
				t.Error("wrong restore version")
			}
			w.Write([]byte(`{}`))
		case "POST /files/f/undelete":
			w.Write([]byte(`{}`))
		case "DELETE /files/f/purge", "DELETE /files/trash", "DELETE /shares/s":
			w.WriteHeader(204)
		case "GET /files/f/shares":
			w.Write([]byte(`[{"share_id":"s","kind":"link"}]`))
		case "POST /files/f/share":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["access"] != "read" {
				t.Error("unexpected permissions")
			}
			if calls[key] == 1 {
				if body["link"] != true || body["expires_in_seconds"] != float64(86400) {
					t.Errorf("link request: %v", body)
				}
			} else if body["email"] != "friend@example.test" || body["link"] != nil || body["expires_in_seconds"] != nil {
				t.Errorf("email request: %v", body)
			}
			w.WriteHeader(201)
			w.Write([]byte(`{"share_id":"s","token":"secret"}`))
		default:
			t.Errorf("unexpected %s", key)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := NewUnscoped(srv.URL, "device")
	ctx := context.Background()
	trash, err := c.Trash(ctx)
	if err != nil || len(trash) != 1 || trash[0].Size != nil {
		t.Fatalf("trash: %v %v", trash, err)
	}
	versions, err := c.Versions(ctx, "f")
	if err != nil || len(versions) != 1 || versions[0].Version != 42 {
		t.Fatalf("versions: %v %v", versions, err)
	}
	for _, fn := range []func() error{
		func() error { return c.Undelete(ctx, "f") }, func() error { return c.RestoreVersion(ctx, "f", 42) },
		func() error { return c.Purge(ctx, "f") }, func() error { return c.EmptyTrash(ctx) },
	} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.CreateShare(ctx, "f", "", 1)
	if err != nil || result.URL != srv.URL+"/s/secret" {
		t.Fatalf("share: %v %v", result, err)
	}
	if _, err = c.CreateShare(ctx, "f", "friend@example.test", 0); err != nil {
		t.Fatal(err)
	}
	shares, err := c.Shares(ctx, "f")
	if err != nil || len(shares) != 1 || shares[0].ID != "s" {
		t.Fatalf("shares: %v %v", shares, err)
	}
	if err = c.RevokeShare(ctx, "s"); err != nil {
		t.Fatal(err)
	}
}
func TestRecoveryPreservesServerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/token" {
			w.Write([]byte(`{"token":"J"}`))
			return
		}
		w.WriteHeader(409)
		w.Write([]byte(`{"error":"name taken"}`))
	}))
	defer srv.Close()
	err := NewUnscoped(srv.URL, "device").Undelete(context.Background(), "f")
	var status *StatusError
	if !errors.As(err, &status) || status.Code != 409 {
		t.Fatalf("lost server failure: %v", err)
	}
}
