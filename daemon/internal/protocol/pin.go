package protocol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ErrCertificateChanged means the server presented a certificate other than the one the
// user trusted at pairing. Every platform's UI matches on this message.
var ErrCertificateChanged = errors.New("server certificate changed")

// CertInfo describes a server's leaf certificate, for the user to decide whether to trust it.
type CertInfo struct {
	Host        string
	Fingerprint string // SHA-256 of the DER bytes, see [Fingerprint]
	Subject     string
	Issuer      string
	NotAfter    time.Time
	SelfSigned  bool
	Trusted     bool // verifies against the system roots for Host
}

// Fingerprint is the SHA-256 of a certificate's DER bytes as uppercase hex pairs joined by
// ':' — the same text `openssl x509 -noout -fingerprint -sha256` prints, so the user can
// compare it against the server side.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	b.Grow(len(h) + len(h)/2)
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

// normalizePin drops separators and case, so a pin copied from openssl, a UI or a config
// file in any of its usual spellings compares equal.
func normalizePin(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ':', ' ', '\t', '\n', '\r':
			return -1
		}
		if r >= 'a' && r <= 'z' {
			return r - 'a' + 'A'
		}
		return r
	}, s)
}

// SameFingerprint reports whether two fingerprints name the same certificate. An empty
// fingerprint matches nothing.
func SameFingerprint(a, b string) bool {
	na, nb := normalizePin(a), normalizePin(b)
	return na != "" && na == nb
}

// pinnedVerifier implements the acceptance rule for a client that holds a pin: a chain the
// system trusts for host is accepted as usual; otherwise the leaf must be exactly the
// pinned certificate. Hostname and expiry are deliberately not checked in the second case —
// the user trusted that exact certificate. host comes from the URL, not the handshake: for
// an IP address the handshake carries no server name, and verifying without one would
// accept any publicly trusted certificate.
func pinnedVerifier(host, pin string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("server sent no certificate")
		}
		leaf := cs.PeerCertificates[0]
		if systemTrusted(leaf, cs.PeerCertificates[1:], host) {
			return nil
		}
		got := Fingerprint(leaf.Raw)
		if SameFingerprint(pin, got) {
			return nil
		}
		return fmt.Errorf("%w: expected %s, got %s", ErrCertificateChanged, pin, got)
	}
}

func systemTrusted(leaf *x509.Certificate, intermediates []*x509.Certificate, host string) bool {
	pool := x509.NewCertPool()
	for _, c := range intermediates {
		pool.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: pool})
	return err == nil
}

func certAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// FetchCertificate reads the server's leaf certificate without sending any request, so the
// user can be shown what they are asked to trust. Verification is off only for this
// handshake, which carries nothing; Trusted reports what a strict client would decide.
func FetchCertificate(ctx context.Context, serverURL string) (*CertInfo, error) {
	u, err := url.Parse(serverURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" {
		return nil, ErrInsecureServerURL
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	host := u.Hostname()
	d := tls.Dialer{NetDialer: defaultDialer(), Config: &tls.Config{
		ServerName:         host,
		CurvePreferences:   curvePreferences,
		InsecureSkipVerify: true, //nolint:gosec // only reads the certificate; nothing is sent over this connection
	}}
	conn, err := d.DialContext(ctx, "tcp", certAddr(u))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("server sent no certificate")
	}
	leaf := certs[0]
	return &CertInfo{
		Host:        u.Host,
		Fingerprint: Fingerprint(leaf.Raw),
		Subject:     nameOf(leaf.Subject.CommonName, leaf.Subject.String()),
		Issuer:      nameOf(leaf.Issuer.CommonName, leaf.Issuer.String()),
		NotAfter:    leaf.NotAfter,
		SelfSigned:  bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil,
		Trusted:     systemTrusted(leaf, certs[1:], host),
	}, nil
}

// nameOf prefers the common name; certificates without one are described by their full
// distinguished name rather than shown blank.
func nameOf(cn, full string) string {
	if cn != "" {
		return cn
	}
	return full
}
