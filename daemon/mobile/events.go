package mobile

import (
	"context"
	"time"
)

// EventListener is what the app implements to hear that something changed on the server.
// OnChange is called from a background goroutine, once per server event; the app decides
// when to run a pass (coalesce a burst, hand it to a worker).
type EventListener interface {
	OnChange()
}

// StartEvents holds a connection to the server's event stream and calls l.OnChange on
// every event, reconnecting with backoff (1 s, doubling to 30 s) whenever the stream
// drops, until StopEvents or Close. Calling it twice replaces the previous listener.
//
// The stream needs a live process: on Android that means while the app is visible or a
// sync pass runs in the foreground. Everything else is the periodic worker's job.
func (c *Client) StartEvents(l EventListener) {
	c.StopEvents()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.eventsCancel = cancel
	c.eventsDone = make(chan struct{})
	done := c.eventsDone
	c.mu.Unlock()
	go func() {
		defer close(done)
		backoff := time.Second
		for ctx.Err() == nil {
			err := c.client.ListenEvents(ctx, func() {
				backoff = time.Second // a working stream resets the clock
				l.OnChange()
			})
			if ctx.Err() != nil || err == nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}()
}

// StopEvents drops the event stream and waits for the listener goroutine to end, so no
// OnChange arrives after it returns. Safe to call when nothing is running.
func (c *Client) StopEvents() {
	c.mu.Lock()
	cancel, done := c.eventsCancel, c.eventsDone
	c.eventsCancel, c.eventsDone = nil, nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
