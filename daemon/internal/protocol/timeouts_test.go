package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A phone that goes to the background gets frozen, and its TCP connections die without
// either side being told. A request already in flight then waits forever — nothing arrives
// and nothing errors — which is what left the app sitting on the pairing screen after a
// pairing the server had already approved. Every call must be bounded.

func TestDefaultDialerBoundsConnectAndProbesIdleConnections(t *testing.T) {
	d := defaultDialer()
	if d.Timeout <= 0 {
		t.Errorf("dial timeout must be set, got %v", d.Timeout)
	}
	ka := d.KeepAliveConfig
	if !ka.Enable || ka.Idle <= 0 || ka.Interval <= 0 || ka.Count <= 0 {
		t.Errorf("keep-alive probing must be configured, got %+v", ka)
	}
	// A dead peer has to surface well inside the pairing poll's grace window, or the poll
	// gives up before the connection is ever reported as broken.
	if worst := ka.Idle + time.Duration(ka.Count)*ka.Interval; worst >= pairPollNetGrace {
		t.Errorf("a dead peer takes %v to surface, longer than the %v pairing grace", worst, pairPollNetGrace)
	}
}

// Whole transfers must stay unbounded: uploads send 8 MiB chunks and the response headers
// only arrive once a chunk is fully sent, so a client-wide timeout would abort healthy
// uploads on a slow mobile link.
func TestDefaultHTTPClientDoesNotCapWholeTransfers(t *testing.T) {
	hc := defaultHTTPClient()
	if hc.Timeout != 0 {
		t.Errorf("http.Client.Timeout must stay unset, got %v", hc.Timeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", hc.Transport)
	}
	if tr.ResponseHeaderTimeout != 0 {
		t.Errorf("ResponseHeaderTimeout must stay unset, got %v", tr.ResponseHeaderTimeout)
	}
	if tr.DialContext == nil {
		t.Error("DialContext must come from defaultDialer, so connects are bounded")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("TLS handshake must be bounded")
	}
}

// Refresh right after pairing runs while the app is still coming back to the foreground,
// on connections that may already be dead. It has to fail rather than hang: a hung refresh
// is what kept the browser from ever appearing.
func TestChangesGivesUpOnAServerThatNeverAnswers(t *testing.T) {
	restore := changesTimeout
	changesTimeout = 40 * time.Millisecond
	defer func() { changesTimeout = restore }()

	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "jwt-123"})
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer close(release)

	c := New(srv.URL, "kfd_test")
	errc := make(chan error, 1)
	go func() {
		_, _, _, err := c.Changes(context.Background(), 0, 500)
		errc <- err
	}()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected an error from a server that never answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Changes blocked on a server that never answered")
	}
}
