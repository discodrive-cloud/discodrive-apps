package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func browseMux(changes, fileBody string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "0" {
			w.Write([]byte(changes))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 2, "has_more": false})
	})
	mux.HandleFunc("GET /files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fileBody))
	})
	return httptest.NewServer(mux)
}

type entry struct {
	ID, Name, LocalPath   string
	IsDir, Cached, Pinned bool
}

func TestBrowseRefreshListDownloadPin(t *testing.T) {
	changes := `{"changes":[
		{"seq":1,"op":"create","node_id":"d1","path":"docs","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false},
		{"seq":2,"op":"create","node_id":"f1","path":"docs/a.txt","is_dir":false,"version":1,"content_hash":"","size":5,"deleted":false}
	],"cursor":2,"has_more":false}`
	srv := browseMux(changes, "hello")
	defer srv.Close()
	root := t.TempDir()
	b, err := NewBrowser(srv.URL, "kfd", root, filepath.Join(t.TempDir(), "i.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := b.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	var rootList []entry
	js, _ := b.List("")
	json.Unmarshal([]byte(js), &rootList)
	if len(rootList) != 1 || rootList[0].Name != "docs" || !rootList[0].IsDir {
		t.Fatalf("root: %s", js)
	}

	var docs []entry
	js, _ = b.List("d1")
	json.Unmarshal([]byte(js), &docs)
	if len(docs) != 1 || docs[0].ID != "f1" || docs[0].Name != "a.txt" {
		t.Fatalf("docs: %s", js)
	}

	path, err := b.Download("f1")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "hello" {
		t.Fatalf("content: %q", got)
	}
	if filepath.Base(path) != "a.txt" {
		t.Fatalf("path: %s", path)
	}

	js, _ = b.List("d1")
	json.Unmarshal([]byte(js), &docs)
	if !docs[0].Cached {
		t.Fatal("after download cached must be true")
	}

	if err := b.Pin("f1"); err != nil {
		t.Fatal(err)
	}
	js, _ = b.List("d1")
	json.Unmarshal([]byte(js), &docs)
	if !docs[0].Pinned {
		t.Fatal("after pin pinned must be true")
	}

	if err := b.RemoveLocal("f1"); err != nil {
		t.Fatal(err)
	}
	js, _ = b.List("d1")
	json.Unmarshal([]byte(js), &docs)
	if docs[0].Cached {
		t.Fatal("after RemoveLocal cached must be false")
	}
}

// ExistsWithHash is what keeps auto-upload from overwriting: the server accepts a same-named
// upload as a new version of the existing node, so the client has to decide "skip / rename"
// before sending. Anything it cannot prove identical must read as "different".
func TestBrowserExistsWithHash(t *testing.T) {
	changes := `{"changes":[
		{"seq":1,"op":"create","node_id":"d1","path":"photos","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false},
		{"seq":2,"op":"create","node_id":"f1","path":"photos/a.jpg","is_dir":false,"version":1,"content_hash":"H1","size":5,"deleted":false},
		{"seq":3,"op":"create","node_id":"d2","path":"photos/sub","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false},
		{"seq":4,"op":"create","node_id":"f2","path":"photos/nohash.jpg","is_dir":false,"version":1,"content_hash":"","size":7,"deleted":false},
		{"seq":5,"op":"create","node_id":"f3","path":"top.jpg","is_dir":false,"version":1,"content_hash":"H9","size":3,"deleted":false}
	],"cursor":5,"has_more":false}`
	srv := browseMux(changes, "")
	defer srv.Close()
	b, err := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Refresh(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		parent, file string
		sha          string
		want         string
	}{
		{"same content", "d1", "a.jpg", "H1", "same"},
		{"other content", "d1", "a.jpg", "H2", "different"},
		{"not there", "d1", "b.jpg", "H1", "absent"},
		{"directory in the way", "d1", "sub", "H1", "different"},
		{"server hash unknown", "d1", "nohash.jpg", "H1", "different"},
		{"local hash unknown", "d1", "a.jpg", "", "different"},
		{"root parent", "", "top.jpg", "H9", "same"},
		{"root parent, absent", "", "nope.jpg", "H9", "absent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.ExistsWithHash(tc.parent, tc.file, tc.sha)
			if err != nil {
				t.Fatalf("ExistsWithHash: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ExistsWithHash(%q,%q,%q) = %q, want %q", tc.parent, tc.file, tc.sha, got, tc.want)
			}
		})
	}
}

// An unknown parent node id is a programming error in the caller, not a reason to report
// "absent" and let the upload overwrite something.
func TestBrowserExistsWithHashUnknownParent(t *testing.T) {
	srv := browseMux(`{"changes":[],"cursor":0,"has_more":false}`, "")
	defer srv.Close()
	b, err := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.ExistsWithHash("ghost", "a.jpg", "H1"); err == nil {
		t.Fatal("want an error for an unknown parent node id")
	}
}

func TestBrowserRelPath(t *testing.T) {
	changes := `{"changes":[
		{"seq":1,"op":"create","node_id":"d1","path":"docs","is_dir":true,"version":1,"content_hash":"","size":0,"deleted":false}
	],"cursor":1,"has_more":false}`
	srv := browseMux(changes, "")
	defer srv.Close()
	b, err := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := b.RelPath("d1"); got != "docs" {
		t.Fatalf("RelPath(d1) = %q, want docs", got)
	}
	if got := b.RelPath("nope"); got != "" {
		t.Fatalf("RelPath(nope) = %q, want empty", got)
	}
}
