package protocol

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"strings"
)

// Events opens an SSE stream at /sync/events. The caller reads the body; most callers want
// [Client.ListenEvents] instead.
func (c *Client) Events(ctx context.Context) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, "/sync/events")
}

// ListenEvents holds a /sync/events connection and calls notify on every event the server
// sends, until the stream ends or ctx is cancelled. A stream that ends is reported as an
// error (io.EOF included) so the caller can decide whether to reconnect; a cancelled ctx
// returns ctx.Err().
func (c *Client) ListenEvents(ctx context.Context, notify func()) error {
	resp, err := c.Events(ctx)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp, "GET /sync/events")
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if strings.HasPrefix(sc.Text(), "data:") {
			notify()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errStreamEnded
}

// errStreamEnded is what ListenEvents returns when the server closes an events stream
// cleanly — still a reason to reconnect, never a reason to stop.
var errStreamEnded = errors.New("GET /sync/events: stream ended")
