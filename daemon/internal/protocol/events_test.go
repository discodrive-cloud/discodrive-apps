package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// An events server that streams the given events, then either hangs until the request
// ends or closes the stream.
func eventsServer(t *testing.T, events int, closeAfter bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"jwt"}`))
	})
	mux.HandleFunc("GET /sync/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		w.Write([]byte(": hello\n\n"))
		fl.Flush()
		for i := 0; i < events; i++ {
			w.Write([]byte("data: {\"seq\":1}\n\n"))
			fl.Flush()
		}
		if closeAfter {
			return
		}
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestListenEventsNotifiesPerEventAndStopsWithContext(t *testing.T) {
	srv := eventsServer(t, 3, false)
	c := New(srv.URL, "kfd")
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- c.ListenEvents(ctx, func() {
			if n.Add(1) == 3 {
				cancel()
			}
		})
	}()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenEvents did not return after the context was cancelled")
	}
	if n.Load() != 3 {
		t.Fatalf("notified %d times, want 3", n.Load())
	}
}

func TestListenEventsReportsAStreamTheServerClosed(t *testing.T) {
	srv := eventsServer(t, 1, true)
	c := New(srv.URL, "kfd")
	var n atomic.Int32
	err := c.ListenEvents(context.Background(), func() { n.Add(1) })
	if err == nil {
		t.Fatal("a closed stream must be an error, so the caller reconnects")
	}
	if n.Load() != 1 {
		t.Fatalf("notified %d times, want 1", n.Load())
	}
}

func TestListenEventsFailsOnAnUnexpectedStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"jwt"}`))
	})
	mux.HandleFunc("GET /sync/events", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "kfd")
	if err := c.ListenEvents(context.Background(), func() { t.Fatal("notified on a 503") }); err == nil {
		t.Fatal("want an error on 503")
	}
}
