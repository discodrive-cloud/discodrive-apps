// Package diagnostics provides opt-in, bounded on-device diagnostics for embedded clients.
package diagnostics

import (
	"os"
	"path/filepath"
	"sync"
)

type Writer struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	size  int64
	Limit int64
}

// Configure closes the previous file before switching. Empty path disables all writes.
func (w *Writer) Configure(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	w.path = ""
	w.size = 0
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.path = path
	w.size = info.Size()
	return nil
}
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return len(p), nil
	}
	limit := w.Limit
	if limit <= 0 {
		limit = 2 << 20
	}
	if w.size > 0 && w.size+int64(len(p)) > limit {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		w.file = nil
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return 0, err
		}
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	// A single giant error must not grow a diagnostic file without bound.
	data := p
	if int64(len(data)) > limit {
		data = data[:limit]
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	if err != nil {
		return n, err
	}
	return len(p), nil
}
