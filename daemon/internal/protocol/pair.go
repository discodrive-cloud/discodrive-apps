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

func PairPoll(ctx context.Context, serverURL, deviceCode string, interval time.Duration) (string, error) {
	var netErrSince time.Time
	for {
		body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/pair/token", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := defaultHTTPClient().Do(req)
		if err != nil {
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
		var out struct {
			Status      string `json:"status"`
			DeviceToken string `json:"device_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		switch out.Status {
		case "approved":
			return out.DeviceToken, nil
		case "pending":
		default:
			return "", fmt.Errorf("pairing not completed: %s", out.Status)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}
