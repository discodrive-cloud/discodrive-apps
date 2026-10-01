package protocol

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedClientIgnoresInsecureEnvironment(t *testing.T) {
	t.Setenv("DISCODRIVE_INSECURE_TLS", "1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted server reached") }))
	defer server.Close()
	if _, err := NewStrict(server.URL, "token").SyncMeta(context.Background()); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
}

// The environment must never weaken TLS for any constructor: a variable in a launchd plist
// or a shell profile would otherwise turn certificate checks off in a signed application.
func TestConstructorsIgnoreInsecureEnvironment(t *testing.T) {
	t.Setenv("DISCODRIVE_INSECURE_TLS", "1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted server reached") }))
	defer server.Close()
	for name, c := range map[string]*Client{
		"New":         New(server.URL, "token"),
		"NewUnscoped": NewUnscoped(server.URL, "token"),
	} {
		if _, err := c.SyncMeta(context.Background()); err == nil {
			t.Errorf("%s accepted an untrusted certificate", name)
		}
	}
	if _, err := PairInit(context.Background(), server.URL, "n", "desktop"); err == nil {
		t.Error("PairInit accepted an untrusted certificate")
	}
}

func TestCheckServerURL(t *testing.T) {
	ok := []string{"https://drive.example.com", "https://1.2.3.4:8443/", "http://localhost", "http://localhost:8080",
		"http://127.0.0.1:9000", "http://[::1]:8080", "HTTP://LOCALHOST:1"}
	bad := []string{"http://example.com", "http://192.168.1.10:8080", "http://localhost.evil.com", "ftp://x",
		"smb://host/share", "", "https://", "javascript:alert(1)", "http://user@localhost"}
	for _, u := range ok {
		if err := CheckServerURL(u); err != nil {
			t.Errorf("CheckServerURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range bad {
		if err := CheckServerURL(u); err == nil {
			t.Errorf("CheckServerURL(%q) = nil, want error", u)
		}
	}
}

func TestPairInitRejectsPlainHTTP(t *testing.T) {
	_, err := PairInit(context.Background(), "http://example.com", "n", "desktop")
	if err == nil || !strings.Contains(err.Error(), "server URL must use https") {
		t.Fatalf("PairInit(http://example.com) = %v, want https error", err)
	}
	if _, err := PairPoll(context.Background(), "http://example.com", "dc", 0); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("PairPoll(http://example.com) = %v, want https error", err)
	}
}
