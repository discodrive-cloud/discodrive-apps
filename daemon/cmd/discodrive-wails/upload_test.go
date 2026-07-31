package main

import (
	"bytes"
	"context"
	"io"
	"sort"
	"testing"
	"time"
)

// fakeUploadAPI is an in-memory UploadAPI double: it stores chunks by index and tracks
// the next expected index for resume.
type fakeUploadAPI struct {
	chunks    map[int][]byte
	next      int
	completed bool
	initSize  int64 // size declared at UploadInit, so tests can assert it is passed through
	// initModTime is the content date declared at UploadInit, likewise asserted by tests.
	initModTime time.Time
}

func newFakeUploadAPI() *fakeUploadAPI { return &fakeUploadAPI{chunks: map[int][]byte{}} }

func (f *fakeUploadAPI) UploadInit(_ context.Context, _, _ string, size int64, modTime time.Time) (string, int, error) {
	f.initSize = size
	f.initModTime = modTime
	return "u", 0, nil
}
func (f *fakeUploadAPI) UploadChunk(_ context.Context, _ string, n int, r io.Reader, onSent func(int64)) (int, error) {
	b, _ := io.ReadAll(r)
	if onSent != nil {
		onSent(int64(len(b)))
	}
	f.chunks[n] = b
	if n+1 > f.next {
		f.next = n + 1
	}
	return f.next, nil
}
func (f *fakeUploadAPI) UploadStatus(_ context.Context, _ string) (int, error) { return f.next, nil }
func (f *fakeUploadAPI) UploadComplete(_ context.Context, _ string) error {
	f.completed = true
	return nil
}
func (f *fakeUploadAPI) UploadAbort(_ context.Context, _ string) error { return nil }

func (f *fakeUploadAPI) reassemble() []byte {
	keys := make([]int, 0, len(f.chunks))
	for k := range f.chunks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var out []byte
	for _, k := range keys {
		out = append(out, f.chunks[k]...)
	}
	return out
}

func blob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i * 13) % 251)
	}
	return b
}

func TestUploaderSendsAllChunksAndCompletes(t *testing.T) {
	data := blob(20 << 20) // 20 MiB
	f := newFakeUploadAPI()
	u := NewUploader(f)
	u.chunkSize = 4 << 20 // 5 chunks

	wantMod := time.Date(2019, 6, 15, 12, 30, 0, 0, time.UTC)
	var lastSent, lastTotal int64
	err := u.Upload(context.Background(), "p", "a.bin", bytes.NewReader(data), int64(len(data)), wantMod,
		func(sent, total int64) { lastSent, lastTotal = sent, total })
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !f.completed {
		t.Fatalf("Complete was not called")
	}
	// The declared size is what lets the server reject a session whose chunks fall short.
	if f.initSize != int64(len(data)) {
		t.Fatalf("UploadInit got size=%d, want %d", f.initSize, len(data))
	}
	// The content date has to reach Init, or the file lands dated "now" on the server.
	if !f.initModTime.Equal(wantMod) {
		t.Fatalf("UploadInit got modTime=%s, want %s", f.initModTime, wantMod)
	}
	if !bytes.Equal(f.reassemble(), data) {
		t.Fatalf("reassembled %d bytes != input %d", len(f.reassemble()), len(data))
	}
	if lastSent != int64(len(data)) || lastTotal != int64(len(data)) {
		t.Fatalf("final progress sent=%d total=%d, want %d/%d", lastSent, lastTotal, len(data), len(data))
	}
}

func TestUploaderResumesFromServerNextChunk(t *testing.T) {
	data := blob(20 << 20)
	f := newFakeUploadAPI()
	f.next = 2 // server says chunks 0,1 already received
	u := NewUploader(f)
	u.chunkSize = 4 << 20

	if err := u.Upload(context.Background(), "p", "a.bin", bytes.NewReader(data), int64(len(data)), time.Time{}, nil); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, ok := f.chunks[0]; ok {
		t.Fatalf("chunk 0 should have been skipped on resume")
	}
	if _, ok := f.chunks[1]; ok {
		t.Fatalf("chunk 1 should have been skipped on resume")
	}
	for n := 2; n <= 4; n++ {
		if _, ok := f.chunks[n]; !ok {
			t.Fatalf("chunk %d should have been sent on resume", n)
		}
	}
}
