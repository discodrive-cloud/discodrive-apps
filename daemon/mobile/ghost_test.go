package mobile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/index"
)

// A ghost is a node the local index still lists after the server hard-deleted it and the
// delete event was lost. The server answers 404 {"error":"not found"} about it; the browser
// then forgets the node and its subtree, and never touches the rest of the tree.
func ghostBrowser(t *testing.T, gone map[string]bool) *Browser {
	t.Helper()
	changes := `{"changes":[
		{"seq":1,"op":"create","node_id":"new1","path":"new1","is_dir":true,"version":1,"deleted":false},
		{"seq":2,"op":"create","node_id":"app","path":"new1/DiscoDrive.app","is_dir":true,"version":1,"deleted":false},
		{"seq":3,"op":"create","node_id":"plist","path":"new1/DiscoDrive.app/Info.plist","is_dir":false,"version":1,"size":5,"deleted":false},
		{"seq":4,"op":"create","node_id":"keep","path":"new1/keep.txt","is_dir":false,"version":1,"size":4,"deleted":false},
		{"seq":5,"op":"create","node_id":"D","path":"Docs","is_dir":true,"version":1,"deleted":false},
		{"seq":6,"op":"create","node_id":"Da","path":"Docs/a.txt","is_dir":false,"version":1,"deleted":false},
		{"seq":7,"op":"create","node_id":"d","path":"docs","is_dir":true,"version":1,"deleted":false},
		{"seq":8,"op":"create","node_id":"db","path":"docs/b.txt","is_dir":false,"version":1,"deleted":false}
	],"cursor":8,"has_more":false}`
	notFound := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
	ok := func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": r.PathValue("id"), "version": 2})
	}
	byID := func(w http.ResponseWriter, r *http.Request) {
		if gone[r.PathValue("id")] {
			notFound(w)
			return
		}
		ok(w, r)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "0" {
			w.Write([]byte(changes))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 8, "has_more": false})
	})
	mux.HandleFunc("GET /files/{id}", byID)
	mux.HandleFunc("GET /files/{id}/content", byID)
	mux.HandleFunc("PATCH /files/{id}/rename", byID)
	mux.HandleFunc("GET /files/{id}/versions", byID)
	mux.HandleFunc("DELETE /files/{id}", byID)
	// Sharing and restoring answer the same 404 for an unknown recipient or version as for
	// a missing node.
	mux.HandleFunc("POST /files/{id}/share", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Email *string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gone[r.PathValue("id")] || (body.Email != nil && *body.Email == "nobody@x.test") {
			notFound(w)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"share_id": "s1", "access": "read"})
	})
	mux.HandleFunc("POST /files/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version int64 `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gone[r.PathValue("id")] || body.Version == 99 {
			notFound(w)
			return
		}
		ok(w, r)
	})
	mux.HandleFunc("PATCH /files/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ParentID *string `json:"parent_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gone[r.PathValue("id")] || (body.ParentID != nil && gone[*body.ParentID]) {
			notFound(w)
			return
		}
		ok(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	b, err := NewBrowser(srv.URL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	if err := b.Refresh(); err != nil {
		t.Fatal(err)
	}
	return b
}

func indexed(b *Browser, id string) bool {
	_, ok, _ := b.idx.Get(id)
	return ok
}

func checkIndex(t *testing.T, b *Browser, gone, kept []string) {
	t.Helper()
	for _, id := range gone {
		if indexed(b, id) {
			t.Errorf("%s still in the index", id)
		}
	}
	for _, id := range kept {
		if !indexed(b, id) {
			t.Errorf("%s dropped from the index, want it kept", id)
		}
	}
}

func TestBrowserDeleteOfGhostSucceeds(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"app": true, "plist": true})
	if err := b.Delete("app"); err != nil {
		t.Fatalf("delete of a ghost: %v, want success", err)
	}
	checkIndex(t, b, []string{"app", "plist"}, []string{"new1", "keep"})
}

func TestBrowserRenameOfGhostReportsGone(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"app": true, "plist": true})
	err := b.Rename("app", "x.app")
	if err == nil || !strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("rename of a ghost: %v, want %q", err, NodeGoneMarker)
	}
	checkIndex(t, b, []string{"app", "plist"}, []string{"new1", "keep"})
}

func TestBrowserDownloadOfGhostReportsGone(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"plist": true})
	if _, err := b.Download("plist"); err == nil || !strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("download of a ghost: %v, want %q", err, NodeGoneMarker)
	}
	checkIndex(t, b, []string{"plist"}, []string{"app", "new1", "keep"})
}

func TestBrowserVersionsOfGhostReportsGone(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"plist": true})
	if _, err := b.Versions("plist"); err == nil || !strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("versions of a ghost: %v, want %q", err, NodeGoneMarker)
	}
	checkIndex(t, b, []string{"plist"}, []string{"app", "new1", "keep"})
}

// Moving into a ghost folder: the folder is what is gone, not the file being moved.
func TestBrowserMoveIntoGhostForgetsTheFolder(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"app": true, "plist": true})
	if err := b.Move("keep", "app"); err == nil || !strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("move into a ghost: %v, want %q", err, NodeGoneMarker)
	}
	checkIndex(t, b, []string{"app", "plist"}, []string{"new1", "keep"})
}

// "Docs" and "docs" are distinct siblings on the server; forgetting one keeps the other.
func TestBrowserForgettingAGhostKeepsACaseSibling(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"D": true, "Da": true})
	if err := b.Delete("D"); err != nil {
		t.Fatalf("delete of a ghost: %v", err)
	}
	checkIndex(t, b, []string{"D", "Da"}, []string{"d", "db", "new1", "keep"})
}

// A ghost and a live folder at the same path: forgetting the ghost keeps the live one and
// its children.
func TestBrowserForgettingAGhostKeepsALiveNodeAtTheSamePath(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"A": true})
	if err := b.idx.Batch(func(bt *index.Batch) error {
		for _, n := range []index.Node{
			{NodeID: "A", RelPath: "P", IsDir: true, Version: 1},
			{NodeID: "Ax", RelPath: "P/x", Version: 1},
			{NodeID: "B", RelPath: "P", IsDir: true, Version: 1},
			{NodeID: "By", RelPath: "P/y", Version: 1},
		} {
			if err := bt.Put(n); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete("A"); err != nil {
		t.Fatalf("delete of a ghost: %v", err)
	}
	checkIndex(t, b, []string{"A"}, []string{"B", "By", "new1", "keep"})
}

// A 404 about something else the request names — an unknown recipient, a missing version —
// is not the node being gone: the node stays in the index and the error is not "gone".
func TestBrowserShareToUnknownUserKeepsTheNode(t *testing.T) {
	b := ghostBrowser(t, nil)
	if _, err := b.CreateShare("keep", "nobody@x.test", 1); err == nil || strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("share to an unknown user: %v, want a plain error", err)
	}
	checkIndex(t, b, nil, []string{"keep", "new1"})
}

func TestBrowserRestoreOfMissingVersionKeepsTheNode(t *testing.T) {
	b := ghostBrowser(t, nil)
	if err := b.RestoreVersion("keep", 99); err == nil || strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("restore of a missing version: %v, want a plain error", err)
	}
	checkIndex(t, b, nil, []string{"keep", "new1"})
}

func TestBrowserShareOfGhostReportsGone(t *testing.T) {
	b := ghostBrowser(t, map[string]bool{"plist": true})
	if _, err := b.CreateShare("plist", "someone@x.test", 1); err == nil || !strings.Contains(err.Error(), NodeGoneMarker) {
		t.Fatalf("share of a ghost: %v, want %q", err, NodeGoneMarker)
	}
	if err := b.RestoreVersion("plist", 1); err == nil {
		t.Fatal("restore of a forgotten ghost: want an error")
	}
	checkIndex(t, b, []string{"plist"}, []string{"app", "new1", "keep"})
}
