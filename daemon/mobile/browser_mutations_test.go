package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserMutations(t *testing.T) {
	hits := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("POST /files/folder", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		hits["folder"] = b["name"].(string)
		json.NewEncoder(w).Encode(map[string]any{"id": "nd", "name": b["name"], "is_dir": true, "version": 1})
	})
	mux.HandleFunc("POST /files/upload", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		hits["upload"] = r.FormValue("name")
		json.NewEncoder(w).Encode(map[string]any{"id": "nu", "name": r.FormValue("name"), "is_dir": false, "version": 1})
	})
	mux.HandleFunc("PATCH /files/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		hits["rename"] = r.PathValue("id")
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "x", "is_dir": false, "version": 2})
	})
	mux.HandleFunc("PATCH /files/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		hits["move"] = r.PathValue("id")
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "name": "x", "is_dir": false, "version": 2})
	})
	mux.HandleFunc("DELETE /files/{id}", func(w http.ResponseWriter, r *http.Request) {
		hits["delete"] = r.PathValue("id")
		w.WriteHeader(http.StatusNoContent)
	})
	// auto-Refresh after each mutation hits /sync/changes; return empty deltas.
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b, _ := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	defer b.Close()

	if err := b.Mkdir("", "newdir"); err != nil || hits["folder"] != "newdir" {
		t.Fatalf("mkdir: %v %v", err, hits)
	}
	tmp := filepath.Join(t.TempDir(), "u.txt")
	os.WriteFile(tmp, []byte("x"), 0o644)
	if err := b.Upload(tmp, "p1"); err != nil || hits["upload"] != "u.txt" {
		t.Fatalf("upload: %v %v", err, hits)
	}
	if err := b.Rename("n9", "b.txt"); err != nil || hits["rename"] != "n9" {
		t.Fatalf("rename: %v %v", err, hits)
	}
	if err := b.Move("n9", "p2"); err != nil || hits["move"] != "n9" {
		t.Fatalf("move: %v %v", err, hits)
	}
	if err := b.Delete("n9"); err != nil || hits["delete"] != "n9" {
		t.Fatalf("delete: %v %v", err, hits)
	}
}

// EnsureFolder is called once per auto-upload pass, so the common case — the destination
// already exists — must not cost a round trip, and the first-run case must return an id the
// uploads can be addressed to.
func TestBrowserEnsureFolder(t *testing.T) {
	created := 0
	changes := `{"changes":[
		{"seq":1,"op":"create","node_id":"d1","path":"DeviceUploads","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false},
		{"seq":2,"op":"create","node_id":"f1","path":"taken.jpg","is_dir":false,"version":1,"content_hash":"H1","size":3,"deleted":false}
	],"cursor":2,"has_more":false}`
	after := `{"changes":[
		{"seq":3,"op":"create","node_id":"d2","path":"DeviceUploads/Pixel 8","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false}
	],"cursor":3,"has_more":false}`

	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("since") == "0":
			w.Write([]byte(changes))
		case created > 0 && r.URL.Query().Get("since") == "2":
			w.Write([]byte(after))
		default:
			json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 3, "has_more": false})
		}
	})
	mux.HandleFunc("POST /files/folder", func(w http.ResponseWriter, r *http.Request) {
		created++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]any{"id": "d2", "name": body["name"], "is_dir": true, "version": 1})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	b, _ := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	defer b.Close()
	if err := b.Refresh(); err != nil {
		t.Fatal(err)
	}

	id, err := b.EnsureFolder("", "DeviceUploads")
	if err != nil {
		t.Fatalf("existing folder: %v", err)
	}
	if id != "d1" {
		t.Fatalf("existing folder id = %q, want d1", id)
	}
	if created != 0 {
		t.Fatalf("created = %d — an existing folder must not be re-created", created)
	}

	id, err = b.EnsureFolder("d1", "Pixel 8")
	if err != nil {
		t.Fatalf("new folder: %v", err)
	}
	if id != "d2" {
		t.Fatalf("new folder id = %q, want d2", id)
	}
	if created != 1 {
		t.Fatalf("created = %d, want exactly one create", created)
	}

	// A name held by a file is not a destination; saying "fine" here would send every
	// photo of the batch into an overwrite of that file.
	if _, err := b.EnsureFolder("", "taken.jpg"); err == nil {
		t.Fatal("want an error when the name belongs to a file")
	}
}
