package mobile

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type countingListener struct{ n atomic.Int32 }

func (l *countingListener) OnChange() { l.n.Add(1) }

// A server whose event stream sends one event per connection and then closes it, so the
// listener has to reconnect to hear the next one.
func flakyEventsServer(t *testing.T, connections *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"jwt"}`))
	})
	mux.HandleFunc("GET /sync/meta", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"scope_epoch":0}`))
	})
	mux.HandleFunc("GET /sync/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"changes":[],"cursor":0,"has_more":false}`))
	})
	mux.HandleFunc("GET /sync/events", func(w http.ResponseWriter, r *http.Request) {
		connections.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {}\n\n"))
		w.(http.Flusher).Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartEventsNotifiesAndReconnects(t *testing.T) {
	var conns atomic.Int32
	srv := flakyEventsServer(t, &conns)
	c, _ := newClient(t, srv.URL)
	l := &countingListener{}
	c.StartEvents(l)
	// Two events can only come from two connections: the stream is reconnected.
	waitFor(t, "two events across reconnects", func() bool { return l.n.Load() >= 2 })
	if conns.Load() < 2 {
		t.Fatalf("connections = %d, want at least 2", conns.Load())
	}
	c.StopEvents()
	seen := l.n.Load()
	time.Sleep(200 * time.Millisecond)
	if l.n.Load() != seen {
		t.Fatal("OnChange arrived after StopEvents returned")
	}
}

func TestStopEventsIsSafeWithoutAStream(t *testing.T) {
	srv := flakyEventsServer(t, new(atomic.Int32))
	c, _ := newClient(t, srv.URL)
	c.StopEvents()
	c.StopEvents()
}

func TestCloseDropsTheStream(t *testing.T) {
	var conns atomic.Int32
	srv := flakyEventsServer(t, &conns)
	c, _ := newClient(t, srv.URL)
	l := &countingListener{}
	c.StartEvents(l)
	waitFor(t, "first event", func() bool { return l.n.Load() >= 1 })
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	seen := l.n.Load()
	time.Sleep(300 * time.Millisecond)
	if l.n.Load() != seen {
		t.Fatal("the stream outlived Close")
	}
}
