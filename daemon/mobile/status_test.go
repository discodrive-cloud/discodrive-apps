package mobile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"net/url"

	"discodrive.org/daemon/internal/protocol"
)

// The phone showed "offline" when a file could not be renamed into place, which reads as a
// network problem and is not one.
func TestSyncStateSeparatesDiskFailuresFromNetworkOnes(t *testing.T) {
	diskErr := fmt.Errorf("seq 156 (notes/why?.md): %w",
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EPERM})
	if got := syncState(diskErr); got != "error" {
		t.Errorf("disk failure reported as %q, want \"error\"", got)
	}
	if got := syncState(&os.PathError{Op: "open", Path: "x", Err: syscall.ENOSPC}); got != "error" {
		t.Errorf("out of space reported as %q, want \"error\"", got)
	}
	if got := syncState(errors.New("dial tcp: connection refused")); got != "offline" {
		t.Errorf("unreachable server reported as %q, want \"offline\"", got)
	}
}

// A certificate other than the trusted one is not a connection problem, and the app must
// be able to recognise it from the error text alone.
func TestChangedCertificateIsAnErrorWithTheMarkerFirst(t *testing.T) {
	inner := fmt.Errorf("%w: expected AA, got BB", protocol.ErrCertificateChanged)
	err := fmt.Errorf("sync meta: %w", &url.Error{Op: "Get", URL: "https://x/sync/meta", Err: inner})
	if got := syncState(err); got != "error" {
		t.Errorf("changed certificate reported as %q, want \"error\"", got)
	}
	if got := certError(err).Error(); got != "server certificate changed: expected AA, got BB" {
		t.Errorf("certError = %q", got)
	}
	other := errors.New("dial tcp: connection refused")
	if certError(other) != other || certError(nil) != nil {
		t.Error("certError must leave other errors alone")
	}
}
