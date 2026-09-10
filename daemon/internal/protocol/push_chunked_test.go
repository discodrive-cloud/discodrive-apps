package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// A file longer than one chunk goes up through the resumable protocol, addressed by path
// with the base version — so the engine's conflict detection survives the switch — and
// the result carries what a sync PUT would.
func TestPushFileUsesChunksPastTheThreshold(t *testing.T) {
	saved := PushChunkSize
	PushChunkSize = 4 << 10
	defer func() { PushChunkSize = saved }()

	var mu sync.Mutex
	var initBody map[string]any
	var initScope string
	chunks := map[int][]byte{}
	puts := 0
	mux := tokenMux()
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		puts++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "put"}, "conflicted": false})
	})
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_ = json.NewDecoder(r.Body).Decode(&initBody)
		initScope = r.Header.Get(scopeHeader)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "u1", "next_chunk": 0})
	})
	mux.HandleFunc("PUT /upload/{id}/chunk/{n}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n, _ := strconv.Atoi(r.PathValue("n"))
		mu.Lock()
		chunks[n] = body
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"next_chunk": n + 1})
	})
	mux.HandleFunc("GET /upload/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := len(chunks)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"next_chunk": n})
	})
	mux.HandleFunc("POST /upload/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"conflicted": true, "node": map[string]any{"id": "n9", "version": 7}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "dt")
	c.sendScope = true
	payload := make([]byte, 10_000)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	base := int64(3)
	mod := time.Date(2021, 5, 6, 7, 8, 9, 0, time.UTC)
	node, conflicted, err := c.PushFile(context.Background(), "dir/big.bin", &base, bytes.NewReader(payload), mod)
	if err != nil {
		t.Fatalf("PushFile: %v", err)
	}
	if puts != 0 {
		t.Fatal("a file past the threshold went up as one PUT")
	}
	if initBody["path"] != "dir/big.bin" {
		t.Fatalf("init path = %v", initBody["path"])
	}
	if v, _ := initBody["base_version"].(float64); int64(v) != base {
		t.Fatalf("init base_version = %v, want %d", initBody["base_version"], base)
	}
	if v, _ := initBody["size"].(float64); int(v) != len(payload) {
		t.Fatalf("init size = %v", initBody["size"])
	}
	if initBody["modified_at"] == nil {
		t.Fatal("the content date did not travel with the session")
	}
	if initScope != "1" {
		t.Fatal("the scope header is missing from init; the path would resolve against the whole vault")
	}
	var assembled []byte
	for i := 0; i < len(chunks); i++ {
		assembled = append(assembled, chunks[i]...)
	}
	if !bytes.Equal(assembled, payload) {
		t.Fatalf("reassembled %d bytes across %d chunks, want %d", len(assembled), len(chunks), len(payload))
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	if !conflicted || node.NodeID != "n9" || node.Version != 7 {
		t.Fatalf("result = %+v conflicted=%v, want the complete response's node and conflict flag", node, conflicted)
	}
}

func TestPushFileStaysOnePutBelowTheThreshold(t *testing.T) {
	saved := PushChunkSize
	PushChunkSize = 4 << 10
	defer func() { PushChunkSize = saved }()
	puts, inits := 0, 0
	mux := tokenMux()
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		puts++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"node": map[string]any{"id": "put", "version": 1}, "conflicted": false})
	})
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) { inits++ })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "dt")
	if _, _, err := c.PushFile(context.Background(), "small.bin", nil, bytes.NewReader(make([]byte, 4<<10)), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if puts != 1 || inits != 0 {
		t.Fatalf("puts=%d inits=%d, want one PUT and no session for a file that fits in a chunk", puts, inits)
	}
}
