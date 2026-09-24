package mobile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/vault"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"
	"time"
)

type serverIO struct {
	client *protocol.Client
	idx    *index.Index
	root   string
	ctx    context.Context
	cache  *vaultMetadata
}

func (s serverIO) context() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
func (s serverIO) full(sp string) string {
	if sp == "" {
		return s.root
	}
	return path.Join(s.root, sp)
}
func (s serverIO) ListDir(sp string) ([]vault.Entry, error) {
	kids, err := s.idx.Children(s.full(sp))
	if err != nil {
		return nil, err
	}
	out := make([]vault.Entry, 0, len(kids))
	for _, n := range kids {
		out = append(out, vault.Entry{Name: path.Base(n.RelPath), IsDir: n.IsDir})
	}
	return out, nil
}
func (s serverIO) ReadFile(sp string) ([]byte, error) {
	n, ok, err := s.idx.GetByPath(s.full(sp))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("vault: %q not found in index", sp)
	}
	key := fmt.Sprintf("%s:%d:%s", n.NodeID, n.Version, n.ContentHash)
	if s.cache != nil && n.ContentHash != "" {
		s.cache.mu.Lock()
		data, ok := s.cache.values[key]
		s.cache.mu.Unlock()
		if ok {
			return append([]byte(nil), data...), nil
		}
	}
	var buf bytes.Buffer
	var output io.Writer = &buf
	// Metadata is small even for long names. Do not buffer arbitrary remote content.
	if strings.HasSuffix(sp, "dir.c9r") || strings.HasSuffix(sp, "name.c9s") || strings.HasSuffix(sp, ".cryptomator") {
		output = &metadataWriter{buffer: &buf, remaining: 1 << 20}
	}
	if err := s.client.Download(s.context(), n.NodeID, output); err != nil {
		return nil, err
	}
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	if s.cache != nil && strings.EqualFold(hex.EncodeToString(sum[:]), n.ContentHash) && len(data) <= 64<<10 {
		s.cache.mu.Lock()
		if s.cache.size+len(data) <= 8<<20 {
			s.cache.size += len(data) - len(s.cache.values[key])
			s.cache.values[key] = append([]byte(nil), data...)
		}
		s.cache.mu.Unlock()
	}
	return data, nil
}
func (s serverIO) MakeDir(sp string) error {
	_, err := s.client.EnsureDir(s.context(), s.full(sp))
	return err
}
func (s serverIO) WriteFile(sp string, data []byte) error {
	if path.Base(sp) == "dir.c9r" {
		if _, exists, err := s.idx.GetByPath(s.full(sp)); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("vault name is already taken")
		}
	}
	return s.WriteFileStream(sp, bytes.NewReader(data))
}
func (s serverIO) WriteFileStream(sp string, data io.Reader) error {
	var version int64
	if n, ok, err := s.idx.GetByPath(s.full(sp)); err != nil {
		return err
	} else if ok {
		version = n.Version
	}
	node, conflicted, err := s.client.PushFile(s.context(), s.full(sp), &version, data, time.Time{})
	if err != nil {
		return err
	}
	if conflicted {
		if node.NodeID == "" {
			return fmt.Errorf("vault write conflict: server did not identify the conflict copy")
		}
		if err := s.client.DeleteNode(s.context(), node.NodeID); err != nil {
			return fmt.Errorf("vault write conflict: could not remove the encrypted conflict copy: %w", err)
		}
		return fmt.Errorf("vault file changed on the server; refresh and try again")
	}
	return nil
}
func (s serverIO) Remove(sp string) error { return s.client.DeleteRemote(s.context(), s.full(sp)) }

var _ vault.Sink = serverIO{}

type vaultMetadata struct {
	mu     sync.Mutex
	values map[string][]byte
	size   int
}

func (s serverIO) prefetch(paths []string) {
	jobs := make(chan string)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for sp := range jobs {
				if entry, ok, err := s.idx.GetByPath(s.full(sp)); s.context().Err() == nil && err == nil && ok && entry.ContentHash != "" {
					_, _ = s.ReadFile(sp)
				}
			}
		}()
	}
	for _, sp := range paths {
		if s.context().Err() != nil {
			break
		}
		jobs <- sp
	}
	close(jobs)
	workers.Wait()
}
func (s serverIO) prefetchDirectory(hash string) {
	children, err := s.idx.Children(s.full(hash))
	if err != nil {
		return
	}
	var paths []string
	for _, n := range children {
		if !n.IsDir {
			continue
		}
		name := path.Base(n.RelPath)
		if !strings.HasSuffix(name, ".c9r") && !strings.HasSuffix(name, ".c9s") {
			continue
		}
		for _, leaf := range []string{"dir.c9r", "name.c9s"} {
			sp := path.Join(hash, name, leaf)
			if entry, ok, err := s.idx.GetByPath(s.full(sp)); err == nil && ok && !entry.IsDir && entry.Size <= 64<<10 {
				paths = append(paths, sp)
			}
		}
	}
	s.prefetch(paths)
}

// metadataWriter stops oversized metadata before it can exhaust a mobile process.
type metadataWriter struct {
	buffer    *bytes.Buffer
	remaining int
}

func (w *metadataWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, fmt.Errorf("vault metadata exceeds 1 MiB")
	}
	w.remaining -= len(p)
	return w.buffer.Write(p)
}
