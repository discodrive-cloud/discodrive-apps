package protocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// uploadTestServer parses POST /files/upload as real multipart and records the file part.
// firstUnauthorized makes the first attempt return 401 to drive the retry path.
func uploadTestServer(t *testing.T, firstUnauthorized bool) (*httptest.Server, *[][]byte, *[]int64) {
	t.Helper()
	var files [][]byte
	var lengths []int64
	attempts := 0
	mux := tokenMux()
	mux.HandleFunc("POST /files/upload", func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if firstUnauthorized && attempts == 1 {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		length := r.ContentLength
		// A body that arrives truncated fails to parse — reject it and record nothing,
		// exactly as the real handler does. Tests assert on what got recorded.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			t.Errorf("reading file part: %v", err)
		}
		files = append(files, b)
		lengths = append(lengths, length)
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &files, &lengths
}

// TestUploadFileStreamsExactBytes: the streamed multipart body round-trips through a real
// parser byte-for-byte, and Content-Length is exact rather than chunked (-1).
func TestUploadFileStreamsExactBytes(t *testing.T) {
	payload := bytes.Repeat([]byte("xy"), 400_000) // 800 KB
	path := filepath.Join(t.TempDir(), "pic.bin")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	srv, files, lengths := uploadTestServer(t, false)
	c := New(srv.URL, "kfd")
	if err := c.UploadFile(context.Background(), "p1", "pic.bin", f, time.Time{}); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if len(*files) != 1 {
		t.Fatalf("server accepted %d uploads, want 1", len(*files))
	}
	if !bytes.Equal((*files)[0], payload) {
		t.Fatalf("file part is %d bytes, want %d, and the contents differ", len((*files)[0]), len(payload))
	}
	if (*lengths)[0] <= 0 {
		t.Fatalf("Content-Length = %d: the body went out chunked, so a short one would go unnoticed", (*lengths)[0])
	}
	if (*lengths)[0] <= int64(len(payload)) {
		t.Fatalf("Content-Length = %d must exceed the %d-byte payload by the multipart envelope",
			(*lengths)[0], len(payload))
	}
}

// TestUploadFileResendsWholeBodyAfterUnauthorized: the 401 retry has to rebuild the whole
// multipart body from a rewound source, not send what is left of a consumed reader.
func TestUploadFileResendsWholeBodyAfterUnauthorized(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 300_000)
	path := filepath.Join(t.TempDir(), "r.bin")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	srv, files, _ := uploadTestServer(t, true)
	c := New(srv.URL, "kfd")
	if err := c.UploadFile(context.Background(), "", "r.bin", f, time.Time{}); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(*files) != 1 {
		t.Fatalf("server accepted %d uploads, want 1 after the 401 retry", len(*files))
	}
	if !bytes.Equal((*files)[0], payload) {
		t.Fatalf("retry sent %d bytes, want %d: the source was not rewound", len((*files)[0]), len(payload))
	}
}

// failingSeeker is seekable (so replayableBody streams instead of buffering) but fails
// part-way through reading — a disk read error during upload.
type failingSeeker struct {
	size int64
	off  int64
	fail int64
}

func (f *failingSeeker) Read(p []byte) (int, error) {
	if f.off >= f.fail {
		return 0, errors.New("disk read error")
	}
	n := int64(len(p))
	if f.off+n > f.fail {
		n = f.fail - f.off
	}
	for i := int64(0); i < n; i++ {
		p[i] = 'a'
	}
	f.off += n
	return int(n), nil
}

func (f *failingSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.off = offset
	case io.SeekCurrent:
		f.off += offset
	case io.SeekEnd:
		f.off = f.size + offset
	}
	return f.off, nil
}

// TestUploadFileReportsSourceReadError is what makes streaming safe: when the source dies
// mid-body the pipe closes with that error and the upload fails loudly. Silently sending a
// short body would land a truncated file that the server accepts as complete.
func TestUploadFileReportsSourceReadError(t *testing.T) {
	srv, files, _ := uploadTestServer(t, false)
	c := New(srv.URL, "kfd")

	src := &failingSeeker{size: 500_000, fail: 100_000}
	err := c.UploadFile(context.Background(), "", "broken.bin", src, time.Time{})
	if err == nil {
		t.Fatal("upload of a source that fails mid-read must return an error")
	}
	if len(*files) != 0 {
		t.Fatalf("server accepted %d uploads for a failed source, want 0", len(*files))
	}
}
