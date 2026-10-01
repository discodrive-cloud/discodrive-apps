package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"discodrive.org/daemon/internal/i18n"
	"discodrive.org/daemon/internal/protocol"
)

// pairInitTrusting starts pairing strictly. If that fails on a certificate the system does
// not trust, it shows the certificate and pairs with it pinned once the user says so; the
// returned pin is what to save with the device token. Any other failure is returned as it
// was, with no question asked. interactive says whether in is a terminal: piped input
// (`yes | discodrive pair`) is never taken as the user's consent, and is not read at all.
func pairInitTrusting(ctx context.Context, server, name string, in io.Reader, out io.Writer, interactive bool) (protocol.Pairing, string, error) {
	p, err := protocol.PairInit(ctx, server, name, "desktop")
	if err == nil {
		return p, "", nil
	}
	if protocol.CheckServerURL(server) != nil {
		return p, "", err
	}
	cert, cerr := protocol.FetchCertificate(ctx, server)
	if cerr != nil || cert.Trusted {
		return p, "", err
	}
	selfSigned := ""
	if cert.SelfSigned {
		selfSigned = i18n.T("pair_cert_self_signed") + "\n"
	}
	fmt.Fprintf(out, i18n.T("pair_cert_dialog"), cert.Host, cert.Fingerprint, cert.Subject, cert.Issuer,
		cert.NotAfter.Local().Format(time.DateOnly), selfSigned)
	if !interactive {
		fmt.Fprintln(out, i18n.T("pair_cert_needs_terminal"))
		return p, "", errors.New(i18n.T("pair_cert_declined"))
	}
	fmt.Fprint(out, i18n.T("pair_cert_prompt"))
	answer, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
	default:
		fmt.Fprintln(out)
		return p, "", errors.New(i18n.T("pair_cert_declined"))
	}
	p, err = protocol.PairInitPinned(ctx, server, name, "desktop", cert.Fingerprint)
	if err != nil {
		return p, "", err
	}
	return p, cert.Fingerprint, nil
}

// stdinIsTerminal reports whether stdin is a terminal rather than a pipe or a file.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
