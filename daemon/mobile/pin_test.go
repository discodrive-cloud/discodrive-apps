package mobile

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"discodrive.org/daemon/internal/protocol"
)

func pairingTLSServer(t *testing.T, reached *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"uc","verification_uri":"/pair","interval":1,"expires_in":60}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchCertificate(t *testing.T) {
	var reached int
	srv := pairingTLSServer(t, &reached)
	c, err := FetchCertificate(srv.URL)
	if err != nil {
		t.Fatalf("FetchCertificate: %v", err)
	}
	leaf := srv.Certificate()
	if c.Fingerprint != protocol.Fingerprint(leaf.Raw) {
		t.Errorf("Fingerprint = %s", c.Fingerprint)
	}
	if c.Trusted || !c.SelfSigned {
		t.Errorf("Trusted=%v SelfSigned=%v, want false/true", c.Trusted, c.SelfSigned)
	}
	if got, err := time.Parse(time.RFC3339, c.NotAfter); err != nil || !got.Equal(leaf.NotAfter) {
		t.Errorf("NotAfter = %q (%v), want %v in RFC 3339", c.NotAfter, err, leaf.NotAfter)
	}
	if c.Host == "" || c.Subject == "" || c.Issuer == "" {
		t.Errorf("incomplete certificate: %+v", c)
	}
	if reached != 0 {
		t.Error("FetchCertificate sent a request")
	}
	if _, err := FetchCertificate("http://example.com"); err == nil {
		t.Error("FetchCertificate accepted an http:// URL")
	}
}

// The pin is passed per call. A pinned call must not weaken a later call made without it.
func TestPinIsPerCallNotProcessWide(t *testing.T) {
	var reached int
	srv := pairingTLSServer(t, &reached)
	pin := protocol.Fingerprint(srv.Certificate().Raw)

	if _, err := PairBegin(srv.URL, "phone", "android", pin); err != nil {
		t.Fatalf("PairBegin(pinned): %v", err)
	}
	before := reached
	if _, err := PairBegin(srv.URL, "phone", "android", ""); err == nil {
		t.Fatal("PairBegin without a pin accepted an untrusted certificate after a pinned call")
	}
	if reached != before {
		t.Fatal("strict call reached the untrusted server")
	}
}

// The apps only see error text; a mismatch must carry the shared marker and both prints.
func TestWrongPinReportsChangedCertificate(t *testing.T) {
	var reached int
	srv := pairingTLSServer(t, &reached)
	wrong := protocol.Fingerprint([]byte("other"))
	_, err := PairBegin(srv.URL, "phone", "android", wrong)
	if err == nil {
		t.Fatal("PairBegin accepted a certificate that does not match the pin")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, CertificateChangedMarker) || !strings.Contains(msg, wrong) || !strings.Contains(msg, protocol.Fingerprint(srv.Certificate().Raw)) {
		t.Fatalf("error %q must start with %q and name both fingerprints", msg, CertificateChangedMarker)
	}
	if err := RevokeDevice(srv.URL, "kfd", wrong); err == nil || !strings.HasPrefix(err.Error(), CertificateChangedMarker) {
		t.Fatalf("RevokeDevice = %v, want %q first", err, CertificateChangedMarker)
	}
	if reached != 0 {
		t.Fatal("a request reached the server behind the wrong certificate")
	}
}
