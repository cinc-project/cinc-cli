package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// explainedError is a transport error rewritten as a sentence for the user.
// It wraps the original, so errors.Is and errors.As still see it.
type explainedError struct {
	msg string
	err error
}

func (e *explainedError) Error() string { return e.msg }
func (e *explainedError) Unwrap() error { return e.err }

// Explain rewrites the transport failures a user can fix themselves (a
// server certificate no trusted CA signed, a server that isn't listening, a
// host name that doesn't resolve) into a sentence that names the server and
// says what to check. Go's own text for these is a chain of package
// prefixes that buries both. Any other error, including every error the
// server itself returned, is returned unchanged.
func Explain(err error) error {
	if err == nil {
		return nil
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	host := uerr.URL
	// trusted_certs_dir only applies to the Cinc Server; a Supermarket
	// (whose API lives under /api/v1) trusts the system certificates alone.
	supermarket := false
	if u, perr := url.Parse(uerr.URL); perr == nil && u.Host != "" {
		host = u.Host
		supermarket = strings.HasPrefix(u.Path, "/api/v1/")
	}

	var hostErr x509.HostnameError
	var certErr *tls.CertificateVerificationError
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.As(err, &hostErr):
		return &explainedError{err: err, msg: fmt.Sprintf(
			"we couldn't verify the TLS certificate of %s: %v. Check that the server URL in your profile uses the name the certificate was issued for",
			host, hostErr)}
	case errors.As(err, &certErr) && supermarket:
		return &explainedError{err: err, msg: fmt.Sprintf(
			"we couldn't verify the TLS certificate of %s (%v). A Supermarket's certificate has to be signed by a CA your system trusts",
			host, certErr.Err)}
	case errors.As(err, &certErr):
		return &explainedError{err: err, msg: fmt.Sprintf(
			"we couldn't verify the TLS certificate of %s (%v). If the server uses an internal or self-signed CA, put that CA's certificate in ~/.cinc/trusted_certs, or point trusted_certs_dir in your profile at a directory that has it",
			host, certErr.Err)}
	case errors.As(err, &dnsErr):
		return &explainedError{err: err, msg: fmt.Sprintf(
			"we couldn't look up %s (%v). Check the server URL in your profile",
			dnsErr.Name, dnsErr.Err)}
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return &explainedError{err: err, msg: fmt.Sprintf(
			"we couldn't connect to %s (%v). Check that the server is running and that the server URL in your profile is right",
			host, opErr.Err)}
	}
	return err
}
