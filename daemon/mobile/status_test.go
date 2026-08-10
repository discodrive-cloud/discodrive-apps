package mobile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
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
