// Package cincservererlang runs the shared integration suite against the CINC
// Server Erlang stack (erchef) that integration/terraform brings up in AWS.
// It is run by hand, through integration/run-cinc-server-erlang.sh; without a
// target file the test skips, so CI compiles and lints this package but never
// contacts AWS.
package cincservererlang

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	cinc "github.com/cinc-project/cinc-api"

	"github.com/cinc-project/cinc-cli/integration/suite"
)

// readyTimeout bounds the wait for a freshly booted stack. Package install and
// reconfigure take about 5 minutes; the margin covers slow package downloads.
const readyTimeout = 20 * time.Minute

func TestCincServerErlang(t *testing.T) {
	path := targetPath()
	tf, err := loadTarget(path)
	if errors.Is(err, errNoTarget) {
		t.Skipf("%v; run integration/run-cinc-server-erlang.sh", err)
	}
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	waitForStack(t, tf)

	suite.Run(t, suite.Target{
		Name:       "cinc-server-erlang",
		ServerURL:  tf.ServerURL,
		Org:        tf.Org,
		OtherOrg:   tf.OtherOrg,
		Admin:      tf.Admin,
		KeyPath:    tf.KeyPath,
		CACertPath: tf.CACertPath,
		// erchef is the reference: a failure here is a CLI or cinc-api bug
		// until shown otherwise. Entries are only for server limitations,
		// each stating what was observed.
		Gaps: map[string]string{},
	})
}

// waitForStack blocks until the server answers and the bootstrap has created
// the admin and both orgs. The check signs with the admin key through
// cinc-api directly, so a CLI bug cannot masquerade as a stack still booting.
func waitForStack(t *testing.T, tf targetFile) {
	t.Helper()
	caPEM, err := os.ReadFile(tf.CACertPath)
	if err != nil {
		t.Fatalf("read CA certificate: %v", err)
	}
	hc, err := httpClientTrusting(caPEM)
	if err != nil {
		t.Fatalf("CA certificate %s: %v", tf.CACertPath, err)
	}
	keyPEM, err := os.ReadFile(tf.KeyPath)
	if err != nil {
		t.Fatalf("read admin key: %v", err)
	}
	key, err := cinc.ParseKey(keyPEM)
	if err != nil {
		t.Fatalf("parse admin key %s: %v", tf.KeyPath, err)
	}
	c, err := cinc.NewClient(cinc.Config{ServerURL: tf.ServerURL, Org: tf.Org, ClientName: tf.Admin, Key: key}, cinc.WithHTTPClient(hc))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = waitReady(t.Context(), readyTimeout, 15*time.Second, func(ctx context.Context) error {
		if _, _, err := c.Status.Get(ctx); err != nil {
			return err
		}
		for _, org := range []string{tf.Org, tf.OtherOrg} {
			if _, _, err := c.Orgs.Get(ctx, org); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: %v\nDebug the bootstrap with: aws ssm start-session --target <instance_id>, then read /var/log/cloud-init-output.log", tf.ServerURL, err)
	}
}
