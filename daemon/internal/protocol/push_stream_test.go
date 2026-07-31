package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushTestServer answers the token endpoint and records every PUT /sync/file body.
// firstUnauthorized makes the first push attempt return 401, driving the retry path.
func pushTestServer(t *testing.T, firstUnauthorized bool) (*httptest.Server, *[][]byte, *[]int64) {
	t.Helper()
	var bodies [][]byte
	var lengths []int64
	attempts := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt"})
	})
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if firstUnauthorized && attempts == 1 {
			// Drain the body like a real server would before rejecting.
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading body: %v", err)
		}
		bodies = append(bodies, b)
		lengths = append(lengths, r.ContentLength)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"node": map[string]any{"id": "n1", "version": 1}, "conflicted": false})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &bodies, &lengths
}

// TestPushFileStreamsFileAndDeclaresLength: an *os.File is sent straight from disk with a
// real Content-Length, rather than being pulled through memory first.
func TestPushFileStreamsFileAndDeclaresLength(t *testing.T) {
	payload := bytes.Repeat([]byte("kf"), 300_000) // 600 KB, larger than any copy buffer
	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	srv, bodies, lengths := pushTestServer(t, false)
	c := New(srv.URL, "kfd")
	if _, _, err := c.PushFile(context.Background(), "big.bin", nil, f); err != nil {
		t.Fatalf("push: %v", err)
	}

	if len(*bodies) != 1 {
		t.Fatalf("server saw %d successful pushes, want 1", len(*bodies))
	}
	if !bytes.Equal((*bodies)[0], payload) {
		t.Fatalf("server received %d bytes, want %d, and they differ", len((*bodies)[0]), len(payload))
	}
	if (*lengths)[0] != int64(len(payload)) {
		t.Fatalf("Content-Length = %d, want %d (unset means chunked, and a short body would go unnoticed)",
			(*lengths)[0], len(payload))
	}
}

// TestPushFileResendsWholeBodyAfterUnauthorized is the regression this streaming rewrite
// could plausibly break: the 401 retry has to rewind the file. Sending a partial body on
// the second attempt would store a truncated file that the server accepts as complete.
func TestPushFileResendsWholeBodyAfterUnauthorized(t *testing.T) {
	payload := bytes.Repeat([]byte("abcd"), 100_000) // 400 KB
	path := filepath.Join(t.TempDir(), "retry.bin")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	srv, bodies, _ := pushTestServer(t, true)
	c := New(srv.URL, "kfd")
	if _, _, err := c.PushFile(context.Background(), "retry.bin", nil, f); err != nil {
		t.Fatalf("push: %v", err)
	}

	if len(*bodies) != 1 {
		t.Fatalf("server accepted %d pushes, want 1 after the 401 retry", len(*bodies))
	}
	if !bytes.Equal((*bodies)[0], payload) {
		t.Fatalf("retry sent %d bytes, want %d: the body was not rewound",
			len((*bodies)[0]), len(payload))
	}
}

// TestPushFileHandlesNonSeekableReader: a stream that cannot be rewound still has to
// survive the retry, which is the one case worth buffering for.
func TestPushFileHandlesNonSeekableReader(t *testing.T) {
	const payload = "not seekable"
	srv, bodies, lengths := pushTestServer(t, true)
	c := New(srv.URL, "kfd")

	// strings.Reader is seekable; wrapping it in io.NopCloser hides Seek.
	r := io.NopCloser(strings.NewReader(payload))
	if _, _, err := c.PushFile(context.Background(), "x.txt", nil, r); err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(*bodies) != 1 || string((*bodies)[0]) != payload {
		t.Fatalf("server received %q, want %q", *bodies, payload)
	}
	if (*lengths)[0] != int64(len(payload)) {
		t.Fatalf("Content-Length = %d, want %d", (*lengths)[0], len(payload))
	}
}
