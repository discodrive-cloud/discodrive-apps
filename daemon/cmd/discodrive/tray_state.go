//go:build tray

package main

import (
	"sync"
	"time"

	"discodrive.org/daemon/internal/syncer"
)

// trayIndicator drives the menu bar icon from the sync state the way the native app
// does: the logo's silhouette, full while the server answers, dimmed while it does not,
// and alternating between the two while a pass runs. `apply` draws one frame; it is
// what the platform file provides.
type trayIndicator struct {
	apply func(dim bool)
	mu    sync.Mutex
	state syncer.State
	stop  chan struct{}
}

func newTrayIndicator(apply func(dim bool)) *trayIndicator {
	return &trayIndicator{apply: apply}
}

// set switches to a state; the same state again is a no-op, so a status poll every
// couple of seconds does not restart the blink.
func (t *trayIndicator) set(state syncer.State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if state == t.state && t.stop != nil == (state == syncer.StateSyncing) {
		return
	}
	if t.stop != nil {
		close(t.stop)
		t.stop = nil
	}
	t.state = state
	switch state {
	case syncer.StateSyncing:
		t.apply(false)
		stop := make(chan struct{})
		t.stop = stop
		go t.blink(stop)
	case syncer.StateIdle:
		t.apply(false)
	default: // offline, or not known yet
		t.apply(true)
	}
}

func (t *trayIndicator) blink(stop chan struct{}) {
	tick := time.NewTicker(600 * time.Millisecond)
	defer tick.Stop()
	dim := false
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			dim = !dim
			t.apply(dim)
		}
	}
}
