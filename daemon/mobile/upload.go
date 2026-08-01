package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"discodrive.org/daemon/internal/protocol"
)

// ErrUploadSizeMismatch means the server refused to publish because the assembled bytes did
// not match the size declared at init — the file changed while it was being sent (a video
// still recording, a download still landing). The caller should re-stat and start over once,
// then defer the file; retrying the same session can only fail the same way.
var ErrUploadSizeMismatch = errors.New("file changed during upload")

// defaultChunkSize matches the desktop uploader. Each chunk is buffered whole by the
// protocol client, so this is also the per-upload memory cost on the phone.
const defaultChunkSize = 8 << 20

// maxChunkAttempts bounds retries of one chunk before the whole upload is abandoned. A
// resync (409) or a lost session (404) does not count against it — those are handled and
// make forward progress.
const maxChunkAttempts = 3

// uploadFile drives one file through the chunked protocol: init, resume from the server's
// next_chunk, send sequentially, complete. Chunks are read through an io.SectionReader, so
// a retry re-reads from disk instead of holding the file in memory.
//
// It returns ErrUploadSizeMismatch when the server rejects the assembled size, so the
// caller can tell "the file moved under us" apart from a transport failure.
func uploadFile(ctx context.Context, client *protocol.Client, localPath, parentNodeID, name string, chunkSize int64) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Size and date come from the open handle, so they describe the bytes actually being
	// sent rather than whatever the path pointed at when it was queued.
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	size, modTime := fi.Size(), fi.ModTime()

	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}

	id, next, err := client.UploadInit(ctx, parentNodeID, name, size, modTime)
	if err != nil {
		return err
	}

	attempts := 0
	reinited := false
	for int64(next)*chunkSize < size {
		start := int64(next) * chunkSize
		length := min(chunkSize, size-start)
		sr := io.NewSectionReader(f, start, length)

		n, err := client.UploadChunk(ctx, id, next, sr, nil)
		if err == nil {
			attempts = 0
			next = n
			continue
		}

		var se *protocol.StatusError
		switch {
		case errors.As(err, &se) && se.Code == http.StatusNotFound:
			// The session expired (server GC after an hour) or the server restarted.
			// Start a fresh one, once: a second loss is a real problem, not a hiccup.
			if reinited {
				return fmt.Errorf("upload session lost twice: %w", err)
			}
			reinited = true
			id, next, err = client.UploadInit(ctx, parentNodeID, name, size, modTime)
			if err != nil {
				return err
			}
		case errors.As(err, &se) && se.Code == http.StatusConflict:
			// We and the server disagree on the position. The server is authoritative:
			// its next_chunk rides along in the body.
			if k, ok := nextChunkFrom(se.Body); ok {
				next = k
				continue
			}
			k, serr := client.UploadStatus(ctx, id)
			if serr != nil {
				return err
			}
			next = k
		default:
			attempts++
			if attempts >= maxChunkAttempts {
				_ = client.UploadAbort(ctx, id)
				return err
			}
			// A dropped body leaves the server's position unchanged, but ask rather
			// than assume — it also rolls back a partial chunk on its side.
			if k, serr := client.UploadStatus(ctx, id); serr == nil {
				next = k
			}
		}
	}

	if err := client.UploadComplete(ctx, id); err != nil {
		var se *protocol.StatusError
		if errors.As(err, &se) && se.Code == http.StatusBadRequest {
			return fmt.Errorf("%w: %v", ErrUploadSizeMismatch, err)
		}
		return err
	}
	return nil
}

// nextChunkFrom digs the server's next_chunk out of an error payload. Missing or
// unparseable means "no opinion", and the caller falls back to asking /upload/{id}.
func nextChunkFrom(body string) (int, bool) {
	var out struct {
		NextChunk *int `json:"next_chunk"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.NextChunk == nil {
		return 0, false
	}
	return *out.NextChunk, true
}

// uploadDeadline bounds a single file's transfer. Without it a stalled socket would pin the
// foreground service (and the phone's radio) indefinitely.
const uploadDeadline = 2 * time.Hour
