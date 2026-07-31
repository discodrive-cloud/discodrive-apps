package protocol

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPushFileSendsModifiedAt: without this header the server dates the file on arrival,
// so a synced folder loses every original timestamp.
func TestPushFileSendsModifiedAt(t *testing.T) {
	var got string
	mux := tokenMux()
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Modified-At")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"node": map[string]any{"id": "n1", "version": 1}, "conflicted": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	want := time.Date(2019, 6, 15, 12, 30, 0, 0, time.UTC)
	c := New(srv.URL, "kfd")
	if _, _, err := c.PushFile(context.Background(), "a.txt", nil, strings.NewReader("hi"), want); err != nil {
		t.Fatalf("push: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("X-Modified-At = %q, not RFC3339: %v", got, err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("X-Modified-At = %s, want %s", parsed, want)
	}
}

// TestPushFileOmitsZeroModifiedAt: no date to offer means no header, leaving the server's
// existing behaviour untouched.
func TestPushFileOmitsZeroModifiedAt(t *testing.T) {
	seen := true
	mux := tokenMux()
	mux.HandleFunc("PUT /sync/file", func(w http.ResponseWriter, r *http.Request) {
		_, seen = r.Header["X-Modified-At"]
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"node": map[string]any{"id": "n1", "version": 1}, "conflicted": false})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "kfd")
	if _, _, err := c.PushFile(context.Background(), "a.txt", nil, strings.NewReader("hi"), time.Time{}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if seen {
		t.Fatal("X-Modified-At was sent for a zero time")
	}
}

// TestUploadFileSendsModifiedAtField also guards the Content-Length arithmetic: the field
// has to be counted by multipartOverhead as well as written, or the declared length no
// longer matches the body and the request fails outright.
func TestUploadFileSendsModifiedAtField(t *testing.T) {
	var gotField string
	var parseErr error
	mux := tokenMux()
	mux.HandleFunc("POST /files/upload", func(w http.ResponseWriter, r *http.Request) {
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			parseErr = err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				parseErr = err
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if p.FormName() == "modified_at" {
				b, _ := io.ReadAll(p)
				gotField = string(b)
				continue
			}
			_, _ = io.Copy(io.Discard, p)
		}
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	want := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	c := New(srv.URL, "kfd")
	if err := c.UploadFile(context.Background(), "p1", "a.txt", strings.NewReader("hi"), want); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if parseErr != nil {
		t.Fatalf("server could not parse the multipart body: %v", parseErr)
	}
	parsed, err := time.Parse(time.RFC3339Nano, gotField)
	if err != nil {
		t.Fatalf("modified_at field = %q, not RFC3339: %v", gotField, err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("modified_at = %s, want %s", parsed, want)
	}
}

// TestUploadInitSendsModifiedAt covers the chunked path used by the desktop app.
func TestUploadInitSendsModifiedAt(t *testing.T) {
	var body map[string]any
	mux := tokenMux()
	mux.HandleFunc("POST /upload/init", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"upload_id": "u1", "next_chunk": 0})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	c := New(srv.URL, "kfd")
	if _, _, err := c.UploadInit(context.Background(), "p1", "a.bin", 10, want); err != nil {
		t.Fatalf("init: %v", err)
	}
	got, _ := body["modified_at"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("modified_at = %q, not RFC3339: %v", got, err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("modified_at = %s, want %s", parsed, want)
	}
}
