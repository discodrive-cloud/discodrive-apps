package mobile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// uploadSrv is a stand-in for the server's /upload/* endpoints: it assembles chunks in
// order and lets a test inject one failure at a chosen chunk, which is how resume and
// re-init are exercised.
type uploadSrv struct {
	mu sync.Mutex

	inits    []initReq
	assembly map[string]*bytes.Buffer
	next     map[string]int

	failChunk   int  // chunk index to fail on (-1 = never)
	failStatus  int  // status to fail it with
	failed      bool // the injected failure has fired
	completeSt  int  // status for complete (0 = 201)
	completeMsg string
}

type initReq struct {
	Name       string `json:"name"`
	ParentID   string `json:"parent_id"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

func newUploadSrv() *uploadSrv {
	return &uploadSrv{assembly: map[string]*bytes.Buffer{}, next: map[string]int{}, failChunk: -1}
}

func (u *uploadSrv) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"changes": []any{}, "cursor": 0, "has_more": false})
	})
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) {
		var req initReq
		json.NewDecoder(r.Body).Decode(&req)
		u.mu.Lock()
		u.inits = append(u.inits, req)
		id := fmt.Sprintf("u%d", len(u.inits))
		u.assembly[id] = &bytes.Buffer{}
		u.next[id] = 0
		u.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"upload_id": id, "next_chunk": 0})
	})
	mux.HandleFunc("PUT /upload/{id}/chunk/{n}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var n int
		fmt.Sscanf(r.PathValue("n"), "%d", &n)
		u.mu.Lock()
		defer u.mu.Unlock()
		buf, ok := u.assembly[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "upload session not found"})
			return
		}
		if !u.failed && n == u.failChunk {
			u.failed = true
			w.WriteHeader(u.failStatus)
			json.NewEncoder(w).Encode(map[string]any{"error": "injected", "next_chunk": u.next[id]})
			return
		}
		if n < u.next[id] { // already accepted: idempotent, like the real server
			json.NewEncoder(w).Encode(map[string]any{"next_chunk": u.next[id]})
			return
		}
		if n > u.next[id] {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "chunk out of order", "next_chunk": u.next[id]})
			return
		}
		body := new(bytes.Buffer)
		body.ReadFrom(r.Body)
		buf.Write(body.Bytes())
		u.next[id]++
		json.NewEncoder(w).Encode(map[string]any{"next_chunk": u.next[id]})
	})
	mux.HandleFunc("GET /upload/{id}", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		id := r.PathValue("id")
		if _, ok := u.assembly[id]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"next_chunk": u.next[id]})
	})
	mux.HandleFunc("POST /upload/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.completeSt != 0 {
			w.WriteHeader(u.completeSt)
			json.NewEncoder(w).Encode(map[string]string{"error": u.completeMsg})
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "n1", "version": 1}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (u *uploadSrv) landed(t *testing.T) []byte {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	// The last session is the one that finished.
	id := fmt.Sprintf("u%d", len(u.inits))
	return u.assembly[id].Bytes()
}

func newTestBrowser(t *testing.T, serverURL string) *Browser {
	t.Helper()
	b, err := NewBrowser(serverURL, "kfd", t.TempDir(), filepath.Join(t.TempDir(), "i.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// writeFile lays down size bytes of recognisable content and stamps a known mtime.
func writeFile(t *testing.T, size int, mod time.Time) string {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	p := filepath.Join(t.TempDir(), "IMG_1.jpg")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUploadAsSendsWholeFileWithSizeAndDate(t *testing.T) {
	u := newUploadSrv()
	srv := u.start(t)
	b := newTestBrowser(t, srv.URL)
	b.chunkSize = 4 << 10 // several chunks without writing megabytes

	mod := time.Date(2019, 7, 14, 10, 30, 0, 0, time.UTC)
	path := writeFile(t, 20<<10, mod)
	want, _ := os.ReadFile(path)

	if err := b.UploadAs(path, "", "photo.jpg"); err != nil {
		t.Fatalf("UploadAs: %v", err)
	}
	if got := u.landed(t); !bytes.Equal(got, want) {
		t.Fatalf("reassembled %d bytes, want %d (equal=%v)", len(got), len(want), bytes.Equal(got, want))
	}
	if len(u.inits) != 1 {
		t.Fatalf("inits = %d, want 1", len(u.inits))
	}
	init := u.inits[0]
	if init.Name != "photo.jpg" {
		t.Fatalf("name = %q, want the explicit name, not the local basename", init.Name)
	}
	if init.Size != int64(len(want)) {
		t.Fatalf("declared size = %d, want %d", init.Size, len(want))
	}
	got, err := time.Parse(time.RFC3339Nano, init.ModifiedAt)
	if err != nil {
		t.Fatalf("modified_at %q: %v", init.ModifiedAt, err)
	}
	if !got.Equal(mod) {
		t.Fatalf("modified_at = %s, want the file's own mtime %s", got, mod)
	}
}

func TestUploadAsResumesAfterOutOfOrder(t *testing.T) {
	u := newUploadSrv()
	u.failChunk, u.failStatus = 2, http.StatusConflict
	srv := u.start(t)
	b := newTestBrowser(t, srv.URL)
	b.chunkSize = 4 << 10

	path := writeFile(t, 20<<10, time.Now())
	want, _ := os.ReadFile(path)

	if err := b.UploadAs(path, "", "photo.jpg"); err != nil {
		t.Fatalf("UploadAs after a 409: %v", err)
	}
	if got := u.landed(t); !bytes.Equal(got, want) {
		t.Fatalf("resumed upload is corrupt: %d bytes vs %d", len(got), len(want))
	}
	if len(u.inits) != 1 {
		t.Fatalf("inits = %d — a 409 must resync, not re-init", len(u.inits))
	}
}

func TestUploadAsReInitsOnLostSession(t *testing.T) {
	u := newUploadSrv()
	u.failChunk, u.failStatus = 2, http.StatusNotFound
	srv := u.start(t)
	b := newTestBrowser(t, srv.URL)
	b.chunkSize = 4 << 10

	path := writeFile(t, 20<<10, time.Now())
	want, _ := os.ReadFile(path)

	if err := b.UploadAs(path, "", "photo.jpg"); err != nil {
		t.Fatalf("UploadAs after a 404: %v", err)
	}
	if len(u.inits) != 2 {
		t.Fatalf("inits = %d, want 2 (the lost session is re-inited)", len(u.inits))
	}
	if got := u.landed(t); !bytes.Equal(got, want) {
		t.Fatalf("restarted upload is corrupt: %d bytes vs %d", len(got), len(want))
	}
}

// A size mismatch means the file changed under us. The Kotlin layer restarts that file
// once and then defers it, so it has to be able to recognise the case.
func TestUploadAsSurfacesSizeMismatch(t *testing.T) {
	u := newUploadSrv()
	u.completeSt = http.StatusBadRequest
	u.completeMsg = "upload is incomplete: staged bytes do not match the declared size"
	srv := u.start(t)
	b := newTestBrowser(t, srv.URL)
	b.chunkSize = 4 << 10

	err := b.UploadAs(writeFile(t, 8<<10, time.Now()), "", "photo.jpg")
	if err == nil {
		t.Fatal("want an error when the server rejects the assembled size")
	}
	if !errors.Is(err, ErrUploadSizeMismatch) {
		t.Fatalf("err = %v, want it to wrap ErrUploadSizeMismatch", err)
	}
}

func TestUploadAsDoesNotRefresh(t *testing.T) {
	u := newUploadSrv()
	srv := u.start(t)
	b := newTestBrowser(t, srv.URL)
	b.chunkSize = 4 << 10

	// The index cursor only advances on Refresh, so it doubles as the assertion that a
	// batch upload pays one refresh at the end rather than one per file.
	before, err := b.idx.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.UploadAs(writeFile(t, 4<<10, time.Now()), "", "photo.jpg"); err != nil {
		t.Fatal(err)
	}
	after, err := b.idx.Cursor()
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("cursor moved %d → %d: UploadAs must not refresh the index", before, after)
	}
}
