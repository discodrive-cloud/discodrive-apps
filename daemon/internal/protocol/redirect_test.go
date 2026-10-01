package protocol

import (
	"context"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRejectTokenDowngrade(t *testing.T) {
	seen := make(chan string, 1)
	clear := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen <- string(b)
		w.Write([]byte(`{"token":"jwt"}`))
	}))
	defer clear.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, clear.URL, http.StatusTemporaryRedirect)
	}))
	defer secure.Close()
	c := NewPinned(secure.URL, "secret-device-token", Fingerprint(secure.Certificate().Raw))
	_, err := c.token(context.Background())
	select {
	case b := <-seen:
		if strings.Contains(b, "secret-device-token") {
			t.Fatalf("device_token leaked via HTTPS to HTTP redirect: %s; err=%v", b, err)
		}
	default:
	}
}

func TestRedirectsStayOnOriginalServer(t *testing.T) {
	for _, status := range []int{307, 308} {
		for _, mode := range []string{"strict", "pinned"} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				var destinationCalls atomic.Int32
				other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					destinationCalls.Add(1)
					w.Write([]byte(`{"token":"leaked"}`))
				}))
				defer other.Close()
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/auth/device/token" {
						http.Redirect(w, r, "/same", status)
						return
					}
					if r.URL.Path == "/same" {
						http.Redirect(w, r, other.URL+"/capture", status)
						return
					}
				}))
				defer server.Close()
				pin := ""
				if mode == "pinned" {
					pin = Fingerprint(server.Certificate().Raw)
				}
				c := NewPinned(server.URL, "fake-token", pin)
				if mode == "strict" {
					roots := x509.NewCertPool()
					roots.AddCert(server.Certificate())
					c.hc.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
				}
				if _, err := c.token(context.Background()); err == nil {
					t.Fatal("expected cross-origin redirect rejection")
				}
				if destinationCalls.Load() != 0 {
					t.Fatal("request reached another server")
				}
			})
		}
	}
}

func TestSameOriginRedirectCanExchangeToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device/token" {
			http.Redirect(w, r, "/token", 307)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || !strings.Contains(string(b), "fake-token") {
			t.Errorf("body was not preserved: %s %s", r.Method, b)
		}
		w.Write([]byte(`{"token":"session"}`))
	}))
	defer server.Close()
	c := NewPinned(server.URL, "fake-token", Fingerprint(server.Certificate().Raw))
	if got, err := c.token(context.Background()); err != nil || got != "session" {
		t.Fatalf("safe redirect rejected: %q %v", got, err)
	}
}

func TestServerRedirectOriginComparison(t *testing.T) {
	for _, target := range []string{"https://SERVER.test:443/path", "https://server.test/next"} {
		from, _ := http.NewRequest("GET", "https://server.test/start", nil)
		to, _ := http.NewRequest("GET", target, nil)
		if err := checkServerRedirect(to, []*http.Request{from}); err != nil {
			t.Fatalf("same origin refused: %v", err)
		}
	}
	for _, target := range []string{"http://server.test/path", "https://elsewhere.test/path", "https://server.test:444/path", "https://user:password@server.test/path"} {
		from, _ := http.NewRequest("GET", "https://server.test/start", nil)
		to, _ := http.NewRequest("GET", target, nil)
		if err := checkServerRedirect(to, []*http.Request{from}); err == nil {
			t.Fatalf("unsafe redirect accepted: %s", target)
		}
	}
	from, _ := http.NewRequest("GET", "https://server.test/start", nil)
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = from
	}
	if err := checkServerRedirect(from, via); err == nil {
		t.Fatal("unbounded redirect loop")
	}
}
