package mobile

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"time"

	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
	"discodrive.org/daemon/internal/vault"
)

// serverIO implements vault.Sink: vault storage I/O over the server. root is the vault
// folder rel-path within storage (e.g. "MyVault"); sp is slash-relative to the vault root.
type serverIO struct {
	client *protocol.Client
	idx    *index.Index
	root   string
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
	full := s.full(sp)
	n, ok, err := s.idx.GetByPath(full)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("vault: %q not found in index", full)
	}
	var buf bytes.Buffer
	if err := s.client.Download(context.Background(), n.NodeID, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s serverIO) MakeDir(sp string) error {
	_, err := s.client.EnsureDir(context.Background(), s.full(sp))
	return err
}

func (s serverIO) WriteFile(sp string, data []byte) error {
	// Ciphertext assembled in memory — there is no source file whose date to carry, so
	// the server dates it on arrival.
	_, _, err := s.client.PushFile(context.Background(), s.full(sp), nil, bytes.NewReader(data), time.Time{})
	return err
}

func (s serverIO) Remove(sp string) error {
	return s.client.DeleteRemote(context.Background(), s.full(sp))
}

var _ vault.Sink = serverIO{}
