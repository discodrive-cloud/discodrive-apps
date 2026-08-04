package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPairInitAndPoll(t *testing.T) {
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair/init", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Name, Kind string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Kind != "ios" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "ABCD-EFGH",
			"verification_uri": "/app/pair?code=ABCD-EFGH", "interval": 1, "expires_in": 600,
		})
	})
	mux.HandleFunc("POST /pair/token", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&polls, 1) < 3 {
			json.NewEncoder(w).Encode(map[string]any{"status": "pending"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "approved", "device_token": "kfd_xyz"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p, err := PairInit(context.Background(), srv.URL, "iPhone", "ios")
	if err != nil || p.DeviceCode != "dc" || p.UserCode != "ABCD-EFGH" {
		t.Fatalf("init: %+v err=%v", p, err)
	}
	tok, err := PairPoll(context.Background(), srv.URL, p.DeviceCode, 5*time.Millisecond)
	if err != nil || tok != "kfd_xyz" {
		t.Fatalf("poll: tok=%q err=%v", tok, err)
	}
}

// A backgrounded app loses its sockets while the user is off approving the pairing in a
// browser. Those failures must not end the pairing — polling has to resume once the app
// is back in the foreground.
func TestPairPollSurvivesDroppedConnections(t *testing.T) {
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair/token", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&polls, 1) < 3 {
			panic(http.ErrAbortHandler) // drop the connection without a response
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "approved", "device_token": "kfd_xyz"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tok, err := PairPoll(context.Background(), srv.URL, "dc", 5*time.Millisecond)
	if err != nil || tok != "kfd_xyz" {
		t.Fatalf("poll: tok=%q err=%v", tok, err)
	}
}

// The retry above must not hang forever on a server that is genuinely unreachable.
func TestPairPollGivesUpOnSustainedNetworkFailure(t *testing.T) {
	restore := pairPollNetGrace
	pairPollNetGrace = 30 * time.Millisecond
	defer func() { pairPollNetGrace = restore }()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair/token", func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := PairPoll(context.Background(), srv.URL, "dc", 5*time.Millisecond); err == nil {
		t.Fatalf("expected an error once the grace window elapsed")
	}
}

func TestPairPollExpired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair/token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]any{"status": "expired"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := PairPoll(context.Background(), srv.URL, "dc", 5*time.Millisecond); err == nil {
		t.Fatalf("expected an error on expired")
	}
}
