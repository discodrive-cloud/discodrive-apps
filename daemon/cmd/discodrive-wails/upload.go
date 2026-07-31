package main

import (
	"context"
	"io"
)

// UploadAPI is the chunked-upload surface the Uploader needs. *protocol.Client
// satisfies it; tests substitute a fake.
type UploadAPI interface {
	UploadInit(ctx context.Context, parentID, name string, size int64) (uploadID string, next int, err error)
	UploadChunk(ctx context.Context, uploadID string, n int, r io.Reader, onSent func(sent int64)) (next int, err error)
	UploadStatus(ctx context.Context, uploadID string) (next int, err error)
	UploadComplete(ctx context.Context, uploadID string) error
	UploadAbort(ctx context.Context, uploadID string) error
}

// ProgressFn is called after each accepted chunk with cumulative bytes sent and the
// total size.
type ProgressFn func(sent, total int64)

// Uploader drives a single file through the chunked upload protocol with resume and
// per-chunk retry. It is independent of Wails so it can be unit-tested.
type Uploader struct {
	api       UploadAPI
	chunkSize int64
}

const defaultChunkSize = 8 << 20 // 8 MiB

func NewUploader(api UploadAPI) *Uploader {
	return &Uploader{api: api, chunkSize: defaultChunkSize}
}

// Upload sends ra (size bytes) as name under parentID. It inits the session, resumes
// from the server's next_chunk, sends 8 MiB chunks sequentially, retries a failed
// chunk a few times (re-syncing next_chunk), reports progress, then completes.
func (u *Uploader) Upload(ctx context.Context, parentID, name string, ra io.ReaderAt, size int64, progress ProgressFn) error {
	id, next, err := u.api.UploadInit(ctx, parentID, name, size)
	if err != nil {
		return err
	}
	// The server is authoritative about where to resume from.
	if st, serr := u.api.UploadStatus(ctx, id); serr == nil {
		next = st
	}

	attempts := 0
	for int64(next)*u.chunkSize < size {
		start := int64(next) * u.chunkSize
		end := min(start+u.chunkSize, size)
		sr := io.NewSectionReader(ra, start, end-start)

		var onSent func(int64)
		if progress != nil {
			onSent = func(sent int64) { progress(min(start+sent, size), size) }
		}
		n, err := u.api.UploadChunk(ctx, id, next, sr, onSent)
		if err != nil {
			attempts++
			if attempts >= 3 {
				_ = u.api.UploadAbort(ctx, id)
				return err
			}
			if st, serr := u.api.UploadStatus(ctx, id); serr == nil {
				next = st
			}
			continue
		}
		attempts = 0
		next = n
		if progress != nil {
			progress(min(int64(next)*u.chunkSize, size), size)
		}
	}
	return u.api.UploadComplete(ctx, id)
}
