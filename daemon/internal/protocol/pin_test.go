package protocol

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func pinTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/device/token":
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		case "/pair/init":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"uc","verification_uri":"/pair","interval":1,"expires_in":60}`))
		case "/pair/token":
			_, _ = w.Write([]byte(`{"status":"approved","device_token":"kfd_pinned"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// SHA-256("abc"), as `printf abc | openssl dgst -sha256` prints it, in the pin format.
func TestFingerprintFormat(t *testing.T) {
	const want = "BA:78:16:BF:8F:01:CF:EA:41:41:40:DE:5D:AE:22:23:B0:03:61:A3:96:17:7A:9C:B4:10:FF:61:F2:00:15:AD"
	if got := Fingerprint([]byte("abc")); got != want {
		t.Fatalf("Fingerprint = %q, want %q", got, want)
	}
	if len(want) != 95 {
		t.Fatalf("fixture length %d", len(want))
	}
}

func TestSameFingerprintNormalizes(t *testing.T) {
	a := Fingerprint([]byte("abc"))
	same := []string{a, strings.ToLower(a), strings.ReplaceAll(a, ":", ""), " " + strings.ReplaceAll(a, ":", " ") + "\n"}
	for _, b := range same {
		if !SameFingerprint(a, b) {
			t.Errorf("SameFingerprint(%q, %q) = false", a, b)
		}
	}
	for _, b := range []string{"", Fingerprint([]byte("abd")), a[:len(a)-3]} {
		if SameFingerprint(a, b) {
			t.Errorf("SameFingerprint(%q, %q) = true", a, b)
		}
	}
	if SameFingerprint("", "") {
		t.Error("two empty pins must not match")
	}
}

func TestPinnedClientsAcceptTheirPinnedCertificate(t *testing.T) {
	server := pinTestServer(t)
	pin := Fingerprint(server.Certificate().Raw)
	for name, c := range map[string]*Client{
		"NewPinned":         NewPinned(server.URL, "token", pin),
		"NewUnscopedPinned": NewUnscopedPinned(server.URL, "token", strings.ToLower(pin)),
	} {
		if _, err := c.SyncMeta(context.Background()); err != nil {
			t.Errorf("%s: SyncMeta over the pinned certificate: %v", name, err)
		}
	}
	p, err := PairInitPinned(context.Background(), server.URL, "n", "desktop", pin)
	if err != nil {
		t.Fatalf("PairInitPinned: %v", err)
	}
	tok, err := PairPollPinned(context.Background(), server.URL, p.DeviceCode, time.Millisecond, pin)
	if err != nil || tok != "kfd_pinned" {
		t.Fatalf("PairPollPinned = %q, %v", tok, err)
	}
}

func TestPinnedClientsRejectAnotherCertificate(t *testing.T) {
	server := pinTestServer(t)
	got := Fingerprint(server.Certificate().Raw)
	wrong := Fingerprint([]byte("some other certificate"))
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s accepted a certificate that does not match the pin", name)
		}
		if !errors.Is(err, ErrCertificateChanged) {
			t.Fatalf("%s: %v, want ErrCertificateChanged", name, err)
		}
		if !strings.Contains(err.Error(), "server certificate changed") || !strings.Contains(err.Error(), wrong) || !strings.Contains(err.Error(), got) {
			t.Fatalf("%s: %q must name both fingerprints", name, err)
		}
	}
	_, err := NewPinned(server.URL, "token", wrong).SyncMeta(context.Background())
	check("NewPinned", err)
	_, err = NewUnscopedPinned(server.URL, "token", wrong).SyncMeta(context.Background())
	check("NewUnscopedPinned", err)
	_, err = PairInitPinned(context.Background(), server.URL, "n", "desktop", wrong)
	check("PairInitPinned", err)
}

// An empty pin is the strict client: an untrusted certificate is refused, and not as a
// changed certificate (there was nothing to compare against).
func TestEmptyPinIsStrict(t *testing.T) {
	server := pinTestServer(t)
	for name, c := range map[string]*Client{
		"NewPinned":         NewPinned(server.URL, "token", ""),
		"NewUnscopedPinned": NewUnscopedPinned(server.URL, "token", ""),
		"New":               New(server.URL, "token"),
		"NewStrict":         NewStrict(server.URL+"/", "token"),
	} {
		_, err := c.SyncMeta(context.Background())
		if err == nil {
			t.Errorf("%s accepted an untrusted certificate", name)
		} else if errors.Is(err, ErrCertificateChanged) {
			t.Errorf("%s: %v reported as a changed certificate", name, err)
		}
	}
	if _, err := PairInitPinned(context.Background(), server.URL, "n", "desktop", ""); err == nil {
		t.Error("PairInitPinned with no pin accepted an untrusted certificate")
	}
}

// A pin belongs to the client built with it; a strict client built afterwards is not
// weakened by it.
func TestPinIsPerClientNotProcessWide(t *testing.T) {
	server := pinTestServer(t)
	pin := Fingerprint(server.Certificate().Raw)
	if _, err := PairInitPinned(context.Background(), server.URL, "n", "desktop", pin); err != nil {
		t.Fatalf("PairInitPinned: %v", err)
	}
	if _, err := PairInit(context.Background(), server.URL, "n", "desktop"); err == nil {
		t.Fatal("strict PairInit accepted an untrusted certificate after a pinned call")
	}
}

func TestFetchCertificateReadsTheLeaf(t *testing.T) {
	var reached bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer server.Close()
	info, err := FetchCertificate(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("FetchCertificate: %v", err)
	}
	leaf := server.Certificate()
	if info.Fingerprint != Fingerprint(leaf.Raw) {
		t.Errorf("Fingerprint = %s, want %s", info.Fingerprint, Fingerprint(leaf.Raw))
	}
	if info.Trusted {
		t.Error("a test certificate reported as system-trusted")
	}
	if !info.SelfSigned {
		t.Error("the self-signed test certificate not reported as self-signed")
	}
	if !info.NotAfter.Equal(leaf.NotAfter) {
		t.Errorf("NotAfter = %v, want %v", info.NotAfter, leaf.NotAfter)
	}
	if info.Host == "" || !strings.Contains(server.URL, info.Host) {
		t.Errorf("Host = %q for %s", info.Host, server.URL)
	}
	if info.Subject == "" || info.Issuer == "" {
		t.Errorf("Subject %q / Issuer %q must describe the certificate", info.Subject, info.Issuer)
	}
	if reached {
		t.Error("FetchCertificate sent an HTTP request")
	}
}

func TestFetchCertificateRejectsPlainHTTP(t *testing.T) {
	for _, u := range []string{"http://localhost:1", "http://example.com", "ftp://x", "", "https://"} {
		if _, err := FetchCertificate(context.Background(), u); err == nil {
			t.Errorf("FetchCertificate(%q) = nil error", u)
		}
	}
}

func TestFetchCertificateDefaultsToPort443(t *testing.T) {
	if got := certAddr(mustParse(t, "https://drive.example.com")); got != "drive.example.com:443" {
		t.Errorf("certAddr = %q", got)
	}
	if got := certAddr(mustParse(t, "https://[::1]:8443/x")); got != "[::1]:8443" {
		t.Errorf("certAddr = %q", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A changed certificate is not a dropped connection: the poll must stop at once instead of
// retrying it for the whole network grace window.
func TestPairPollStopsOnChangedCertificate(t *testing.T) {
	server := pinTestServer(t)
	done := make(chan error, 1)
	go func() {
		_, err := PairPollPinned(context.Background(), server.URL, "dc", time.Millisecond, Fingerprint([]byte("other")))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCertificateChanged) {
			t.Fatalf("PairPollPinned = %v, want ErrCertificateChanged", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PairPollPinned kept retrying a changed certificate")
	}
}
