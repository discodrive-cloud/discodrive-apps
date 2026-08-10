package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Pairing struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        int
	ExpiresIn       int
}

func PairInit(ctx context.Context, serverURL, name, kind string) (Pairing, error) {
	body, _ := json.Marshal(map[string]string{"name": name, "kind": kind})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/pair/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return Pairing{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return Pairing{}, fmt.Errorf("/pair/init: %s", resp.Status)
	}
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Pairing{}, err
	}
	return Pairing(out), nil
}

// pairPollNetGrace is how long PairPoll keeps polling while every request fails at the
// network layer. Pairing asks the user to leave for a browser, and an app that is no longer
// in the foreground loses its sockets ("software caused connection abort") — treating the
// first such failure as fatal aborted the very pairing the user had gone off to approve.
// Any successful response resets the window, so only an unbroken run this long gives up
// (a server that is simply unreachable, rather than an app that was backgrounded).
// A var, not a const, so the tests can shorten it.
var pairPollNetGrace = 2 * time.Minute

// pairPollRequestTimeout bounds a single /pair/token request. A frozen app's connection can
// stay open and silent — the request in flight is answered by nobody — and without a deadline
// the poll blocks in Do for the life of the process, leaving the app on the pairing screen
// through a pairing the server has already approved. A timed-out request counts as a network
// failure and is retried under the grace window below.
// A var, not a const, so the tests can shorten it.
var pairPollRequestTimeout = 30 * time.Second

// pairPollOnce performs one /pair/token request under its own deadline, returning the
// pairing status and (once approved) the device token.
func pairPollOnce(ctx context.Context, serverURL, deviceCode string) (status, token string, err error) {
	ctx, cancel := context.WithTimeout(ctx, pairPollRequestTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/pair/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := defaultHTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		Status      string `json:"status"`
		DeviceToken string `json:"device_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Status, out.DeviceToken, nil
}

func PairPoll(ctx context.Context, serverURL, deviceCode string, interval time.Duration) (string, error) {
	var netErrSince time.Time
	for {
		status, token, err := pairPollOnce(ctx, serverURL, deviceCode)
		if err != nil {
			// The caller giving up is not a broken connection: it ends the poll now,
			// rather than being retried under the grace window.
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if netErrSince.IsZero() {
				netErrSince = time.Now()
			} else if time.Since(netErrSince) >= pairPollNetGrace {
				return "", err
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(interval):
			}
			continue
		}
		netErrSince = time.Time{}
		switch status {
		case "approved":
			return token, nil
		case "pending":
		default:
			return "", fmt.Errorf("pairing not completed: %s", status)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}
