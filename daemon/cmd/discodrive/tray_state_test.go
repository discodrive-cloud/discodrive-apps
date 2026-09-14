//go:build tray

package main

import (
	"sync"
	"testing"
	"time"

	"discodrive.org/daemon/internal/syncer"
)

func TestTrayIndicatorFollowsTheState(t *testing.T) {
	var mu sync.Mutex
	var frames []bool
	ind := newTrayIndicator(func(dim bool) { mu.Lock(); frames = append(frames, dim); mu.Unlock() })

	ind.set(syncer.StateOffline)
	ind.set(syncer.StateOffline) // a repeated poll draws nothing new
	ind.set(syncer.StateIdle)
	mu.Lock()
	got := append([]bool(nil), frames...)
	mu.Unlock()
	if len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("frames = %v, want [dim, full]", got)
	}

	ind.set(syncer.StateSyncing)
	time.Sleep(1500 * time.Millisecond)
	ind.set(syncer.StateIdle)
	mu.Lock()
	n := len(frames)
	last := frames[len(frames)-1]
	mu.Unlock()
	if n < 5 {
		t.Fatalf("syncing did not alternate: %d frames", n)
	}
	if last {
		t.Fatal("back to idle must end on the full icon")
	}
	time.Sleep(700 * time.Millisecond)
	mu.Lock()
	after := len(frames)
	mu.Unlock()
	if after != n {
		t.Fatal("the blink kept going after leaving the syncing state")
	}
}
