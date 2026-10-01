// Package engine is the sync client core: fetches changes and applies them to disk.
package engine

import (
	"context"
	"errors"
	"io"
)

// ErrNodeNotFound is the server saying that a node addressed by id does not exist: purged,
// in the trash, or never there. The protocol client's status errors match it with
// errors.Is only for the server's own "not found" answer, never for other 404s (an expired
// upload session, a missing blob, a proxy's page). Clients use it to forget a node their
// index still lists because its delete event never reached them.
var ErrNodeNotFound = errors.New("node not found on the server")

// Change is a single entry from the server change feed (mirrors the server's /sync/changes).
type Change struct {
	Seq         int64
	Op          string
	NodeID      string
	RelPath     string // path relative to the sync root (slash-separated)
	IsDir       bool
	Version     int64
	ContentHash string
	Size        int64
	Deleted     bool
}

// Source provides changes and file content (implemented by the HTTP protocol client).
type Source interface {
	Changes(ctx context.Context, since int64, limit int) (changes []Change, cursor int64, hasMore bool, err error)
	Download(ctx context.Context, nodeID string, w io.Writer) error
}

// NodeChecker is an optional Source extension: whether the server still has a node (GET
// /files/{id}). It answers (false, nil) only for the server's own "not found"; anything
// else — the network, a 5xx, a rejected certificate — is an error. The engine uses it to
// settle which of two index rows at one path is live when their seqs cannot (see
// Engine.removeDeleted). The protocol client implements it.
type NodeChecker interface {
	NodeExists(ctx context.Context, nodeID string) (bool, error)
}
