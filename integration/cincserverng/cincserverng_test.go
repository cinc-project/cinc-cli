// Package cincserverng runs the shared integration suite against an in-memory
// cinc-server-ng on every pull request and before every release. Signature
// checks and ACL enforcement are both on, so the server verifies every request
// the cinc binary sends and refuses what its actor may not do, as erchef does.
//
// The suite runs twice: over plain HTTP, and over HTTPS with cinc-server-ng
// serving TLS itself, as a production server does.
package cincserverng

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cinc-project/cinc-server-ng/server"

	"github.com/cinc-project/cinc-cli/integration/suite"
)

const (
	org      = "cinccli"
	otherOrg = "cinccli-other"
)

// plainTarget and tlsTarget are set by TestMain once the servers are up.
var plainTarget, tlsTarget suite.Target

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run starts one plain-HTTP and one TLS server for the whole package: every
// case gives its objects unique names, so they share each server safely and
// in parallel. The TLS server serves a certificate from a throwaway CA, which
// the CLI trusts through ~/.cinc/trusted_certs as a user would.
func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "cinc-server-ng-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	ca, err := newTestCA(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "test CA: %v\n", err)
		return 1
	}

	var stops []func()
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()
	for _, tc := range []struct {
		name   string
		opts   server.Options
		caCert string
		into   *suite.Target
	}{
		{"cinc-server-ng", server.Options{}, "", &plainTarget},
		{"cinc-server-ng (TLS)", server.Options{TLSCertFile: ca.certFile, TLSKeyFile: ca.keyFile}, ca.caFile, &tlsTarget},
	} {
		tgt, stop, err := startTarget(tc.name, tc.opts, filepath.Join(dir, fmt.Sprintf("admin-%d.pem", len(stops))))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		stops = append(stops, stop)
		tgt.CACertPath = tc.caCert
		*tc.into = tgt
	}
	return m.Run()
}

// startTarget starts a server with opts, writing its admin key to keyPath,
// and returns the suite target for it and a func that stops it.
func startTarget(name string, opts server.Options, keyPath string) (suite.Target, func(), error) {
	opts.Addr = "127.0.0.1:0"
	opts.Orgs = []string{org, otherOrg}
	opts.EnforceACL = true
	srv, err := server.New(opts)
	if err != nil {
		return suite.Target{}, nil, fmt.Errorf("%s: server.New: %w", name, err)
	}
	if err := srv.Start(); err != nil {
		return suite.Target{}, nil, fmt.Errorf("%s: server.Start: %w", name, err)
	}
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}
	if err := os.WriteFile(keyPath, srv.AdminKey(), 0o600); err != nil {
		stop()
		return suite.Target{}, nil, fmt.Errorf("%s: write admin key: %w", name, err)
	}
	return suite.Target{
		Name:      name,
		ServerURL: srv.URL(),
		Org:       org,
		OtherOrg:  otherOrg,
		Admin:     srv.AdminName(),
		KeyPath:   keyPath,
		// Gaps lists cases cinc-server-ng gets wrong, each naming its upstream
		// issue, and loses an entry once a release fixes it. v0.17.0 has none.
	}, stop, nil
}

func TestCincServerNG(t *testing.T) {
	suite.Run(t, plainTarget)
}

func TestCincServerNGTLS(t *testing.T) {
	suite.Run(t, tlsTarget)
}
