package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"discodrive.org/daemon/internal/config"
	"discodrive.org/daemon/internal/desktop"
	"discodrive.org/daemon/internal/protocol"
)

func trustPairServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	inits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pair/init":
			mu.Lock()
			inits++
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"UC-1","verification_uri":"/pair","interval":1,"expires_in":60}`))
		case "/pair/token":
			_, _ = w.Write([]byte(`{"status":"approved","device_token":"kfd_trusted"}`))
		case "/auth/device/token":
			_, _ = w.Write([]byte(`{"token":"jwt"}`))
		default:
			_, _ = w.Write([]byte(`{"changes":[],"cursor":0,"has_more":false}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() int { mu.Lock(); defer mu.Unlock(); return inits }
}

// Nothing was fetched, so there is nothing to trust: the web view cannot make the backend
// pin a certificate on its own.
func TestTrustAndPairNeedsAFetchedCertificate(t *testing.T) {
	a := &App{}
	if _, err := a.TrustAndPair(); err == nil {
		t.Fatal("TrustAndPair paired without a certificate shown to the user")
	}
}

func TestPairInitOffersTheCertificateAndTrustUsesIt(t *testing.T) {
	srv, inits := trustPairServer(t)
	a := &App{}
	info, err := a.PairInit(srv.URL)
	if err != nil {
		t.Fatalf("PairInit: %v", err)
	}
	want := protocol.Fingerprint(srv.Certificate().Raw)
	if !info.NeedsTrust || info.Certificate == nil || info.Certificate.Fingerprint != want {
		t.Fatalf("PairInit = %+v, want a certificate to trust with %s", info, want)
	}
	if !info.Certificate.SelfSigned || info.Certificate.NotAfter == "" || info.Certificate.Host == "" {
		t.Fatalf("certificate view incomplete: %+v", info.Certificate)
	}
	if info.UserCode != "" || inits() != 0 {
		t.Fatal("strict pairing reached the untrusted server")
	}

	info, err = a.TrustAndPair()
	if err != nil {
		t.Fatalf("TrustAndPair: %v", err)
	}
	if info.NeedsTrust || info.UserCode != "UC-1" || inits() != 1 {
		t.Fatalf("TrustAndPair = %+v (inits %d)", info, inits())
	}

	// The pin travels into the saved profile config with the token.
	if err := a.PairPoll(srv.URL, info.DeviceCode, 1); err != nil {
		t.Fatalf("PairPoll: %v", err)
	}
	profile, _ := desktop.ProfileDir()
	t.Cleanup(func() {
		_ = a.idx.Close()
		_ = os.Remove(desktop.DesktopConfigPath(profile))
		_ = desktop.WipeState(profile)
	})
	cfg, err := config.Load(desktop.DesktopConfigPath(profile))
	if err != nil || cfg.ServerPin != want || cfg.DeviceToken != "kfd_trusted" {
		t.Fatalf("saved config = %+v, %v; want pin %s", cfg, err, want)
	}
}

// A later PairInit that did not need trust forgets the earlier certificate: trusting must
// always be about the certificate just shown.
func TestPairInitForgetsAnEarlierCertificate(t *testing.T) {
	srv, _ := trustPairServer(t)
	a := &App{}
	if info, err := a.PairInit(srv.URL); err != nil || !info.NeedsTrust {
		t.Fatalf("PairInit = %+v, %v", info, err)
	}
	if _, err := a.PairInit("http://example.com"); err == nil {
		t.Fatal("PairInit accepted a plain http server")
	}
	if _, err := a.TrustAndPair(); err == nil {
		t.Fatal("TrustAndPair used a certificate from an earlier attempt")
	}
}
