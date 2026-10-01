package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"discodrive.org/daemon/internal/protocol"
)

func pairTLSServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"uc","verification_uri":"/pair","interval":1,"expires_in":60}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPairInitAsksToTrustAnUntrustedCertificate(t *testing.T) {
	srv := pairTLSServer(t)
	want := protocol.Fingerprint(srv.Certificate().Raw)
	var out bytes.Buffer
	p, pin, err := pairInitTrusting(context.Background(), srv.URL, "laptop", strings.NewReader("y\n"), &out, true)
	if err != nil {
		t.Fatalf("pairInitTrusting: %v", err)
	}
	if pin != want || p.DeviceCode != "dc" {
		t.Fatalf("pin = %q, device code %q; want %q", pin, p.DeviceCode, want)
	}
	shown := out.String()
	for _, s := range []string{want, "Trust this certificate? [y/N]", "Only trust this if it matches the fingerprint of your server's certificate."} {
		if !strings.Contains(shown, s) {
			t.Errorf("prompt lacks %q:\n%s", s, shown)
		}
	}
}

func TestPairInitWithoutConsentDoesNotPin(t *testing.T) {
	srv := pairTLSServer(t)
	for _, answer := range []string{"n\n", "\n", ""} {
		var out bytes.Buffer
		if _, pin, err := pairInitTrusting(context.Background(), srv.URL, "laptop", strings.NewReader(answer), &out, true); err == nil || pin != "" {
			t.Errorf("answer %q: pin %q, err %v; want refusal", answer, pin, err)
		}
	}
}

// A server that fails for another reason gets its own error, and no trust question.
func TestPairInitReportsOtherFailuresAsTheyAre(t *testing.T) {
	var out bytes.Buffer
	_, pin, err := pairInitTrusting(context.Background(), "http://example.com", "laptop", strings.NewReader("y\n"), &out, true)
	if err == nil || pin != "" || out.Len() != 0 {
		t.Fatalf("pin %q, err %v, output %q", pin, err, out.String())
	}
}

// Piped input (`yes | discodrive pair`) must not trust a certificate: the user has to see
// the fingerprint and answer at a terminal.
func TestPairInitNonInteractiveDeclinesWithoutReading(t *testing.T) {
	srv := pairTLSServer(t)
	var out bytes.Buffer
	in := strings.NewReader("y\n")
	_, pin, err := pairInitTrusting(context.Background(), srv.URL, "laptop", in, &out, false)
	if err == nil || pin != "" {
		t.Fatalf("pin %q, err %v; want refusal", pin, err)
	}
	if in.Len() != len("y\n") {
		t.Error("the answer was read from non-interactive input")
	}
	if !strings.Contains(out.String(), "interactive terminal") {
		t.Errorf("no hint about the terminal:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Trust this certificate?") {
		t.Error("asked a question nobody can answer")
	}
}
