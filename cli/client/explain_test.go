package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/cinc-project/cinc-cli/cli/config"
)

// TestExplainUntrustedCertificate checks a server whose certificate no
// trusted CA signed gets a sentence naming the server and the two places a
// CA certificate can go, not Go's raw x509 chain.
func TestExplainUntrustedCertificate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := tlsServer(t)
	raw := listClients(t, profileFor(t, srv))
	if raw == nil {
		t.Fatal("expected a certificate verification error")
	}
	host := strings.TrimPrefix(srv.URL, "https://")

	err := Explain(raw)
	msg := err.Error()
	for _, want := range []string{"we couldn't verify", host, "certificate", "~/.cinc/trusted_certs", "trusted_certs_dir"} {
		if !strings.Contains(msg, want) {
			t.Errorf("explained error %q should mention %q", msg, want)
		}
	}
	if !errors.Is(err, raw) {
		t.Error("the explained error should still wrap the original")
	}
}

// TestExplainConnectionRefused checks a server that is not listening gets a
// sentence naming the address and what to check.
func TestExplainConnectionRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	raw := listClients(t, config.Profile{
		ServerURL:  "http://" + addr,
		Org:        "acme",
		ClientName: "tim",
		KeyPath:    writeKeyFile(t),
	})
	if raw == nil {
		t.Fatal("expected a connection error")
	}

	msg := Explain(raw).Error()
	for _, want := range []string{"we couldn't connect to", addr, "server URL"} {
		if !strings.Contains(msg, want) {
			t.Errorf("explained error %q should mention %q", msg, want)
		}
	}
}

// TestExplainLeavesOtherErrorsAlone checks errors the server itself returned,
// and anything else Explain has nothing to add to, pass through untouched.
func TestExplainLeavesOtherErrorsAlone(t *testing.T) {
	for _, err := range []error{
		nil,
		errors.New("cinc: GET /organizations/acme/nodes/web01: 404: Cannot find nodes web01"),
		&url.Error{Op: "Get", URL: "https://x.example.test", Err: errors.New("EOF")},
	} {
		if got := Explain(err); got != err {
			t.Errorf("Explain(%v) = %v, want it unchanged", err, got)
		}
	}
}

// TestExplainUntrustedSupermarketCertificate keeps the trusted_certs advice
// to the Cinc Server, the only thing trusted_certs_dir applies to.
func TestExplainUntrustedSupermarketCertificate(t *testing.T) {
	raw := &url.Error{Op: "Get", URL: "https://supermarket.example.test/api/v1/cookbooks/nginx", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}
	msg := Explain(raw).Error()
	if !strings.Contains(msg, "supermarket.example.test") || strings.Contains(msg, "trusted_certs") {
		t.Errorf("explained Supermarket error = %q, want the host and no trusted_certs advice", msg)
	}
}
