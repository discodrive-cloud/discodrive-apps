package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Pairing struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        int
	ExpiresIn       int
}

// ErrInsecureServerURL is returned for a server URL that would send the device token in
// clear text (or is not a web URL at all).
var ErrInsecureServerURL = errors.New("server URL must use https")

// CheckServerURL accepts only https server URLs. Plain http is allowed for this machine's
// loopback (localhost, 127.0.0.1, [::1], any port) — a local dev server — and nowhere else:
// on any other host the pairing and every later request would carry the device token in
// clear text.
func CheckServerURL(serverURL string) error {
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return ErrInsecureServerURL
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
	}
	return ErrInsecureServerURL
}

// PairInit starts a device-code pairing with strict TLS validation.
func PairInit(ctx context.Context, serverURL, name, kind string) (Pairing, error) {
	return PairInitPinned(ctx, serverURL, name, kind, "")
}

// PairInitPinned is [PairInit] against a server whose certificate the user chose to trust:
// pin is its fingerprint (see [NewPinned]); "" is strict validation.
func PairInitPinned(ctx context.Context, serverURL, name, kind, pin string) (Pairing, error) {
	if err := CheckServerURL(serverURL); err != nil {
		return Pairing{}, err
	}
	body, _ := json.Marshal(map[string]string{"name": name, "kind": kind})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/pair/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClientFor(serverURL, pin).Do(req)
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
func pairPollOnce(ctx context.Context, serverURL, deviceCode, pin string) (status, token string, err error) {
	ctx, cancel := context.WithTimeout(ctx, pairPollRequestTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/pair/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClientFor(serverURL, pin).Do(req)
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

// PairPoll waits for the pairing to be approved, with strict TLS validation.
func PairPoll(ctx context.Context, serverURL, deviceCode string, interval time.Duration) (string, error) {
	return PairPollPinned(ctx, serverURL, deviceCode, interval, "")
}

// PairPollPinned is [PairPoll] with the certificate pin of [PairInitPinned].
func PairPollPinned(ctx context.Context, serverURL, deviceCode string, interval time.Duration, pin string) (string, error) {
	if err := CheckServerURL(serverURL); err != nil {
		return "", err
	}
	var netErrSince time.Time
	for {
		status, token, err := pairPollOnce(ctx, serverURL, deviceCode, pin)
		if err != nil {
			// The caller giving up is not a broken connection: it ends the poll now,
			// rather than being retried under the grace window.
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// Nor is a certificate other than the trusted one: retrying cannot fix it.
			if errors.Is(err, ErrCertificateChanged) {
				return "", err
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
