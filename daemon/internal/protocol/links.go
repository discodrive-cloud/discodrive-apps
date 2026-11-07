package protocol

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// SaveLink sends a link shared from the phone to Saved → Links and returns the HTTP
// status: the share screen decides from it what to show and whether to retry later.
// An unpaired or revoked device is 401, not an error. sharedAt is when a link that waited
// in the outbox was shared (zero for a fresh share): the server keeps a link archived that
// the user archived after it, so a resent copy cannot undo that.
func (c *Client) SaveLink(ctx context.Context, link, title string, sharedAt time.Time) (int, error) {
	body := map[string]string{"url": link}
	if title != "" {
		body["title"] = title
	}
	if !sharedAt.IsZero() {
		body["shared_at"] = sharedAt.UTC().Format(time.RFC3339)
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/me/links", body)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden) {
		return http.StatusUnauthorized, nil
	}
	if err != nil {
		if err.Error() == "authorization failed" { // doJSON's second 401 in a row
			return http.StatusUnauthorized, nil
		}
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
