// Package mobile is the gomobile-bound facade over the sync core. Every exported type and
// function signature stays within gomobile's bindable type set (no context.Context,
// time.Duration, maps or non-byte slices).
package mobile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"discodrive.org/daemon/internal/engine"
	"discodrive.org/daemon/internal/index"
	"discodrive.org/daemon/internal/protocol"
)

// Every entry point takes the account's certificate pin (the fingerprint the user trusted
// at pairing, "" for none) and hands it to the protocol constructors it builds. Nothing is
// stored process-wide: a pinned call must not weaken a later one made without the pin.

// CertificateChangedMarker starts the error text when the server presents a certificate
// other than the pinned one. gomobile flattens errors to their text, so the apps match on
// it (as with BulkDeleteMarker); the text goes on to name the expected and actual
// fingerprints.
const CertificateChangedMarker = "server certificate changed"

// certError returns a changed-certificate error with its own text, dropping the request
// and operation prefixes wrapped around it, so the app sees CertificateChangedMarker
// first. Other errors are returned as they are.
func certError(err error) error {
	if !errors.Is(err, protocol.ErrCertificateChanged) {
		return err
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if strings.HasPrefix(e.Error(), CertificateChangedMarker) {
			return e
		}
	}
	return err
}

// Certificate is a server's TLS certificate as shown to the user before trusting it.
type Certificate struct {
	Host        string
	Fingerprint string // SHA-256, uppercase hex pairs joined by ':' — the pin to store
	Subject     string
	Issuer      string
	NotAfter    string // RFC 3339
	SelfSigned  bool
	Trusted     bool // the system already trusts it; a failed pairing had another cause
}

// FetchCertificate reads the server's certificate without sending a request. Call it when
// pairing without a pin failed: if the certificate is not Trusted, show it and, once the
// user trusts it, pair again with its Fingerprint as the pin. Call off the UI thread.
func FetchCertificate(serverURL string) (*Certificate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := protocol.FetchCertificate(ctx, serverURL)
	if err != nil {
		return nil, err
	}
	return &Certificate{
		Host:        c.Host,
		Fingerprint: c.Fingerprint,
		Subject:     c.Subject,
		Issuer:      c.Issuer,
		NotAfter:    c.NotAfter.UTC().Format(time.RFC3339),
		SelfSigned:  c.SelfSigned,
		Trusted:     c.Trusted,
	}, nil
}

// RevokeDevice removes this device from the account on the server, so its token stops
// working. Call it on unpairing, before the token is forgotten, off the UI thread. A
// device the server already rejects counts as revoked; other errors mean the server was
// not reached, and the caller may still finish unpairing locally.
func RevokeDevice(serverURL, deviceToken, serverPin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return certError(protocol.NewUnscopedPinned(serverURL, deviceToken, serverPin).RevokeDevice(ctx))
}

// Pairing carries what the app needs to complete device pairing.
type Pairing struct {
	VerificationURL string // absolute URL the app opens in a browser
	UserCode        string // code the user confirms in the browser
	DeviceCode      string // opaque; pass to PairAwait
	IntervalSeconds int    // poll interval hint
}

// PairBegin starts device pairing. deviceKind is "ios" or "android". Call off the UI thread.
// serverPin is "" for a first attempt, or the Fingerprint of a Certificate the user trusted.
func PairBegin(serverURL, deviceName, deviceKind, serverPin string) (*Pairing, error) {
	p, err := protocol.PairInitPinned(context.Background(), serverURL, deviceName, deviceKind, serverPin)
	if err != nil {
		return nil, certError(err)
	}
	return &Pairing{
		VerificationURL: serverURL + p.VerificationURI,
		UserCode:        p.UserCode,
		DeviceCode:      p.DeviceCode,
		IntervalSeconds: p.Interval,
	}, nil
}

// PairAwait blocks until the user approves, returning the device token. Call off the UI thread.
// serverPin must be the one PairBegin used.
func PairAwait(serverURL, deviceCode string, intervalSeconds int, serverPin string) (string, error) {
	if intervalSeconds <= 0 {
		intervalSeconds = 2
	}
	tok, err := protocol.PairPollPinned(context.Background(), serverURL, deviceCode, time.Duration(intervalSeconds)*time.Second, serverPin)
	return tok, certError(err)
}

// Status is a snapshot the app renders. The state machine is driven by SyncOnce.
type Status struct {
	// State is "idle" | "syncing" | "offline" | "error". "offline" means the server could
	// not be reached; "error" means it could, and the pass failed for another reason —
	// reporting those as "offline" too sent people looking at their connection when the
	// real problem was a file that could not be written.
	State        string
	LastSyncUnix int64  // 0 if never synced successfully
	LastError    string // last SyncOnce error text, "" if none
	// SetAside is where the first pass after pairing moved the folder's previous contents,
	// "" if there were none. After pairing the server is the truth: nothing that was in the
	// folder before is uploaded; it is kept next to the folder for the user to sort out.
	SetAside string
}

// Client is a sync handle for one paired device + one sync folder.
type Client struct {
	client *protocol.Client
	eng    *engine.Engine
	idx    *index.Index

	opMu     sync.Mutex
	eventsMu sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	mu       sync.Mutex
	status   Status

	// The event stream, while one is held (see StartEvents).
	eventsCancel context.CancelFunc
	eventsDone   chan struct{}
}

// New builds a sync client. syncDir is the app-sandbox folder to mirror; stateDBPath is a
// writable path for the local index (sqlite). Both come from the app's sandbox.
// serverPin is the fingerprint saved at pairing, "" for none.
func New(serverURL, deviceToken, syncDir, stateDBPath, serverPin string) (*Client, error) {
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		return nil, err
	}
	idx, err := index.Open(stateDBPath)
	if err != nil {
		return nil, err
	}
	// A new token is a new pairing even on the same server. Do not rely on the
	// UI's best-effort database deletion: an interrupted unpair may leave it intact.
	root, err := filepath.Abs(syncDir)
	if err != nil {
		idx.Close()
		return nil, err
	}
	identity := sha256.Sum256([]byte(serverURL + "\n" + deviceToken + "\n" + root))
	if err := idx.BindMirrorPairing(hex.EncodeToString(identity[:])); err != nil {
		idx.Close()
		return nil, err
	}
	if err := idx.SetServerURL(serverURL); err != nil {
		idx.Close()
		return nil, err
	}
	client := protocol.NewPinned(serverURL, deviceToken, serverPin)
	eng := engine.New(client, idx, syncDir)
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{client: client, eng: eng, idx: idx, ctx: ctx, cancel: cancel, status: Status{State: "idle"}}, nil
}

// BulkDeleteMarker appears in the error text when a pass refused to delete a large share of
// the synced files. gomobile flattens Go errors to plain exceptions, so the message is all
// that survives the boundary — the app matches on this to offer confirmation.
const BulkDeleteMarker = "refusing to delete"

// ConfirmBulkDelete lets the next pass carry deletions the safety check stopped. Call it only
// after the user has been told how many files it is about, and what they are.
func (c *Client) ConfirmBulkDelete() {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.ctx.Err() == nil {
		c.eng.ConfirmBulkDelete()
	}
}

// ResetLocalIndex forgets what this device knows about the server's tree, so the next pass
// fetches all of it again. Nothing on the server is touched and nothing local is deleted or
// moved: the folder stays the mirror, unlike after a pairing.
//
// This is the other answer to a sync stopped by the mass-deletion check, and usually the right
// one: the folder went missing rather than the files being deleted, so the fix is to rebuild
// the local copy, not to make the server match a mirror that no longer exists. Files still
// present on disk are uploaded as new ones by the pass that follows.
func (c *Client) ResetLocalIndex() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return err
	}
	return c.eng.ResetIndexKeepingFiles()
}

// SyncOnce runs one sync pass. Blocks; call off the UI thread. Concurrent calls serialize.
func (c *Client) SyncOnce() error {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.status.State = "syncing"
	c.mu.Unlock()

	err := certError(c.syncPass(c.ctx))

	c.mu.Lock()
	defer c.mu.Unlock()
	if aside := c.eng.SetAside(); aside != "" {
		c.status.SetAside = aside
	}
	if err != nil && !engine.OnlyPushFailures(err) {
		c.status.State = syncState(err)
		c.status.LastError = err.Error()
		return err
	}
	// A pass whose only trouble is files the server refused has finished: idle, with the
	// sync time moved on. The refusal stays in LastError and is still returned so the
	// host can show it.
	c.status.State = "idle"
	c.status.LastError = ""
	if err != nil {
		c.status.LastError = err.Error()
	}
	c.status.LastSyncUnix = time.Now().Unix()
	return err
}

// syncPass mirrors internal/syncer.(*Syncer).SyncOnce. It is duplicated here (instead of
// importing syncer) so the mobile bind does not pull in syncer's fsnotify dependency. Keep the
// two in sync: on a scope-epoch change reconcile and skip push; otherwise push then pull.
func (c *Client) syncPass(ctx context.Context) error {
	epoch, err := c.client.SyncMeta(ctx)
	if err != nil {
		return err
	}
	last, err := c.eng.ScopeEpoch()
	if err != nil {
		return err
	}
	if epoch != last {
		return c.eng.ResetForScope(ctx, epoch)
	}
	// Files the server rejected are reported, but they must not keep everyone else's
	// changes from arriving: the pull still runs. A push that failed as a whole
	// (connection, credentials, mass-deletion guard) ends the pass as before.
	pushErr := c.eng.PushLocal(ctx, c.client)
	var failures *engine.PushFailures
	if pushErr != nil && !errors.As(pushErr, &failures) {
		return pushErr
	}
	if pullErr := c.eng.PullOnce(ctx); pullErr != nil {
		return errors.Join(pushErr, pullErr)
	}
	// Returned as it is, so hosts can tell a finished pass with rejected files
	// (engine.OnlyPushFailures) from one that failed.
	return pushErr
}

// syncState classifies a failed pass for the UI. A pass that fell over writing to disk — a
// name the filesystem rejects, no space, no permission — has nothing to do with the network,
// and calling it "offline" sent people checking their connection while the real reason sat in
// the error text right below it.
func syncState(err error) string {
	if errors.Is(err, protocol.ErrCertificateChanged) {
		return "error"
	}
	var pathErr *os.PathError
	var linkErr *os.LinkError
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
		return "error"
	}
	return "offline"
}

// Status returns a copy of the tracked state.
func (c *Client) Status() *Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	return &s
}

// Cancel interrupts network work before an embedding app waits for its borrowers.
func (c *Client) Cancel() { c.cancel() }

// Close drops the event stream, if any, and releases the local index. The Client must not
// be used afterwards.
func (c *Client) Close() error {
	c.cancel()
	c.StopEvents()
	c.opMu.Lock()
	defer c.opMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.idx.Close()
}

// ActivityJSON remains readable while a sync pass is running.
func (c *Client) ActivityJSON() string {
	b, _ := json.Marshal(c.eng.Activity())
	return string(b)
}
